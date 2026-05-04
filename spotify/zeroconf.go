//
// Date: 2026-05-04
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2026 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Spotify Connect zeroconf client. Speaks the addUser protocol
// against a Spotify Connect device on the LAN to claim the device for the
// configured Spotify account, regardless of which user it was previously
// linked to. This is what unblocks the "device disappeared from cloud
// because another household member used it" scenario.
//
// Protocol reference: https://github.com/devgianlu/go-librespot (server side
// implementation). We are the *client* of the same protocol.
//

package spotify

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// dhPrime is the 768-bit MODP prime used by Spotify Connect's DH key
// exchange. Hard-coded constant matching librespot.
var dhPrime = func() *big.Int {
	return new(big.Int).SetBytes([]byte{
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xc9, 0x0f, 0xda, 0xa2, 0x21, 0x68, 0xc2, 0x34,
		0xc4, 0xc6, 0x62, 0x8b, 0x80, 0xdc, 0x1c, 0xd1, 0x29, 0x02, 0x4e, 0x08, 0x8a, 0x67, 0xcc, 0x74,
		0x02, 0x0b, 0xbe, 0xa6, 0x3b, 0x13, 0x9b, 0x22, 0x51, 0x4a, 0x08, 0x79, 0x8e, 0x34, 0x04, 0xdd,
		0xef, 0x95, 0x19, 0xb3, 0xcd, 0x3a, 0x43, 0x1b, 0x30, 0x2b, 0x0a, 0x6d, 0xf2, 0x5f, 0x14, 0x37,
		0x4f, 0xe1, 0x35, 0x6d, 0x6d, 0x51, 0xc2, 0x45, 0xe4, 0x85, 0xb5, 0x76, 0x62, 0x5e, 0x7e, 0xc6,
		0xf4, 0x4c, 0x42, 0xe9, 0xa6, 0x3a, 0x36, 0x20, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	})
}()

// dhGenerator is g=2 — the standard generator for this MODP group.
var dhGenerator = big.NewInt(2)

// GetInfoResponse is the subset of fields we care about from the Spotify
// Connect device's getInfo response. Spotify devices return a much larger
// JSON document; we ignore fields we don't use to stay forward-compatible
// with firmware changes.
type GetInfoResponse struct {
	Status       int    `json:"status"`
	StatusString string `json:"statusString"`
	SpotifyError int    `json:"spotifyError"`
	DeviceID     string `json:"deviceID"`
	RemoteName   string `json:"remoteName"`
	PublicKey    string `json:"publicKey"`
	DeviceType   string `json:"deviceType"`
	// TokenType determines the addUser payload format:
	//   "default" / "credentials" -> decrypted blob is a stored-credentials struct
	//   "accesstoken"             -> decrypted blob is a Spotify OAuth access token
	TokenType  string `json:"tokenType"`
	ActiveUser string `json:"activeUser"`
}

// AddUserResponse mirrors what the device returns after a successful addUser.
type AddUserResponse struct {
	Status       int    `json:"status"`
	StatusString string `json:"statusString"`
	SpotifyError int    `json:"spotifyError"`
}

// ZeroconfClient is a client for the Spotify Connect zeroconf HTTP API. One
// instance per target device. The CPath ("/zc", "/spotifyzc",
// "/spotifyConnect", etc.) varies by manufacturer and is taken from the
// device's mDNS TXT record — but most consumers just construct one from a
// LocalDevice via NewZeroconfClient.
type ZeroconfClient struct {
	BaseURL string
	HTTP    *http.Client
}

// NewZeroconfClient builds a client targeting the supplied Spotify Connect
// device. We default the path to "/zc" because that's what every WiiM in the
// wild uses; for other vendors callers can pass a custom CPath.
func NewZeroconfClient(ip string, port int, cpath string) *ZeroconfClient {
	if cpath == "" {
		cpath = "/zc"
	}
	if !strings.HasPrefix(cpath, "/") {
		cpath = "/" + cpath
	}
	return &ZeroconfClient{
		BaseURL: fmt.Sprintf("http://%s:%d%s", ip, port, cpath),
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

// GetInfo issues the action=getInfo handshake and returns the parsed JSON.
// Returns an error if the device is unreachable or the response isn't valid
// JSON. We do NOT enforce status==101 here so callers can decide how strict
// to be.
func (c *ZeroconfClient) GetInfo(ctx context.Context) (*GetInfoResponse, error) {
	u := c.BaseURL + "?action=getInfo"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build getInfo request: %w", err)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("getInfo: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read getInfo body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("getInfo HTTP %d: %s", resp.StatusCode, string(body))
	}

	var info GetInfoResponse
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("getInfo decode: %w (body=%q)", err, string(body))
	}
	return &info, nil
}

// AddUser claims the device for the given Spotify account. There are two
// wire formats depending on what the device advertises in info.TokenType:
//
//   - "accesstoken" (modern eSDK, e.g. WiiM): blob is the raw OAuth access
//     token in plaintext, clientKey is empty, and an extra form field
//     tokenType=accesstoken is sent. No DH/AES/HMAC envelope.
//
//   - anything else (legacy librespot path): blob is the stored credentials
//     wrapped in DH + AES-128-CTR + HMAC-SHA1, exactly as librespot does.
//
// `payload` is interpreted accordingly: for accesstoken it is the access
// token bytes; for the encrypted path it is whatever the device's eSDK
// expects after decryption (typically a stored-credentials structure).
//
// `username` is the Spotify account ID (not display name).
// `deviceName` is how we identify *ourselves* in the request and shows up
// in device logs / Spotify's UI.
func (c *ZeroconfClient) AddUser(ctx context.Context, info *GetInfoResponse, username, deviceName string, payload []byte) (*AddUserResponse, error) {
	form := url.Values{}
	form.Set("action", "addUser")
	form.Set("userName", username)
	form.Set("deviceName", deviceName)

	if strings.EqualFold(info.TokenType, "accesstoken") {
		// Modern eSDK accesstoken path: no encryption, raw token in blob,
		// empty clientKey, plus an explicit tokenType field.
		form.Set("blob", string(payload))
		form.Set("clientKey", "")
		form.Set("tokenType", "accesstoken")
	} else {
		// Legacy DH/AES envelope path.
		blob, clientKey, err := encryptCredentialsBlob(info.PublicKey, payload)
		if err != nil {
			return nil, err
		}
		form.Set("blob", blob)
		form.Set("clientKey", clientKey)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build addUser request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("addUser: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read addUser body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("addUser HTTP %d: %s", resp.StatusCode, string(body))
	}

	var out AddUserResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("addUser decode: %w (body=%q)", err, string(body))
	}

	if out.Status != 101 {
		return &out, fmt.Errorf("addUser reported error: status=%d statusString=%q spotifyError=%d", out.Status, out.StatusString, out.SpotifyError)
	}

	return &out, nil
}

// encryptCredentialsBlob wraps a stored-credentials payload in the
// DH + AES-128-CTR + HMAC-SHA1 envelope expected by librespot-style devices
// when tokenType is not "accesstoken". Returns (base64 blob, base64 clientKey).
func encryptCredentialsBlob(devicePublicKeyB64 string, payload []byte) (string, string, error) {
	devicePubBytes, err := base64.StdEncoding.DecodeString(devicePublicKeyB64)
	if err != nil {
		return "", "", fmt.Errorf("decode device public key: %w", err)
	}

	// 1. Ephemeral DH keypair (95 random bytes for the private exponent).
	privBytes := make([]byte, 95)
	if _, err := rand.Read(privBytes); err != nil {
		return "", "", fmt.Errorf("dh private key entropy: %w", err)
	}
	priv := new(big.Int).SetBytes(privBytes)
	pub := new(big.Int).Exp(dhGenerator, priv, dhPrime)

	// 2. Shared secret = devicePub ^ priv mod p.
	devicePub := new(big.Int).SetBytes(devicePubBytes)
	shared := new(big.Int).Exp(devicePub, priv, dhPrime).Bytes()

	// 3. baseKey = SHA1(shared)[:16]; derive checksum/encryption keys from it.
	baseKeyArr := sha1.Sum(shared)
	baseKey := baseKeyArr[:16]

	mac := hmac.New(sha1.New, baseKey)
	mac.Write([]byte("checksum"))
	checksumKey := mac.Sum(nil)

	mac = hmac.New(sha1.New, baseKey)
	mac.Write([]byte("encryption"))
	encryptionKey := mac.Sum(nil)[:16]

	// 4. AES-128-CTR encrypt with random IV.
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return "", "", fmt.Errorf("iv entropy: %w", err)
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", "", fmt.Errorf("aes init: %w", err)
	}
	encrypted := make([]byte, len(payload))
	cipher.NewCTR(block, iv).XORKeyStream(encrypted, payload)

	// 5. HMAC-SHA1 over the ciphertext with the checksum key.
	mac = hmac.New(sha1.New, checksumKey)
	mac.Write(encrypted)
	checksum := mac.Sum(nil)

	// 6. Wire format: iv || encrypted || checksum, base64.
	var blobBuf bytes.Buffer
	blobBuf.Write(iv)
	blobBuf.Write(encrypted)
	blobBuf.Write(checksum)

	return base64.StdEncoding.EncodeToString(blobBuf.Bytes()),
		base64.StdEncoding.EncodeToString(pub.Bytes()),
		nil
}

