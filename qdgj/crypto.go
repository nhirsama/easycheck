package qdgj

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

const (
	systemID = "ly-zhxg"
)

func validateCredentials(username, password string) error {
	if username == "" || password == "" {
		return errors.New("账号和密码不能为空")
	}
	if len(utf16.Encode([]rune(username))) > 20 || len(utf16.Encode([]rune(password))) > 20 {
		return errors.New("账号和密码不能超过页面的 20 个 UTF-16 字符限制")
	}
	return nil
}

func makeLoginForm(publicKeyBase64, username, password, captcha string, now time.Time) (url.Values, error) {
	if err := validateCredentials(username, password); err != nil {
		return nil, err
	}
	key, err := parseLoginPublicKey(publicKeyBase64)
	if err != nil {
		return nil, err
	}
	encrypted, err := encryptLegacyPKCS1v15(key, frontendPasswordBytes(password))
	if err != nil {
		return nil, fmt.Errorf("RSA 加密密码：%w", err)
	}
	encodedPassword := base64.StdEncoding.EncodeToString(encrypted)
	timestamp := strconv.FormatInt(now.UnixMilli(), 10)
	digestInput := "username=" + username + ",password=" + encodedPassword + ",timestamp=" + timestamp
	form := url.Values{
		"username": {username}, "password": {encodedPassword}, "encryptTransfer": {"true"},
		"timestamp": {timestamp}, "digest": {frontendDigest(digestInput)}, "systemId": {systemID},
	}
	if captcha != "" {
		form.Set("captcha", captcha)
	}
	return form, nil
}

func parseLoginPublicKey(publicKeyBase64 string) (*rsa.PublicKey, error) {
	publicKeyBase64 = strings.TrimSpace(publicKeyBase64)
	var der []byte
	if block, _ := pem.Decode([]byte(publicKeyBase64)); block != nil {
		der = block.Bytes
	} else {
		var err error
		der, err = base64.StdEncoding.DecodeString(publicKeyBase64)
		if err != nil {
			return nil, err
		}
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return x509.ParsePKCS1PublicKey(der)
	}
	key, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("登录公钥不是 RSA 公钥")
	}
	return key, nil
}

// encryptLegacyPKCS1v15 构造 PKCS#1 v1.5 填充并执行 RSA 公钥模幂运算，
// 兼容遗留登录协议使用的公钥长度。
func encryptLegacyPKCS1v15(key *rsa.PublicKey, message []byte) ([]byte, error) {
	if key == nil || key.N == nil || key.N.Sign() <= 0 || key.E < 3 {
		return nil, errors.New("无效的 RSA 公钥")
	}
	k := key.Size()
	if k < 11 || len(message) > k-11 {
		return nil, rsa.ErrMessageTooLong
	}
	em := make([]byte, k)
	em[1] = 2
	padding := em[2 : k-len(message)-1]
	if _, err := io.ReadFull(rand.Reader, padding); err != nil {
		return nil, err
	}
	for i := range padding {
		for padding[i] == 0 {
			if _, err := io.ReadFull(rand.Reader, padding[i:i+1]); err != nil {
				return nil, err
			}
		}
	}
	copy(em[k-len(message):], message)
	result := new(big.Int).Exp(new(big.Int).SetBytes(em), big.NewInt(int64(key.E)), key.N)
	return result.FillBytes(make([]byte, k)), nil
}

func frontendPasswordBytes(password string) []byte {
	var encoded []byte
	for _, unit := range utf16.Encode([]rune(password)) {
		switch {
		case unit < 0x80:
			encoded = append(encoded, byte(unit))
		case unit < 0x800:
			encoded = append(encoded, byte(unit>>6)|0xc0, byte(unit&0x3f)|0x80)
		default:
			encoded = append(encoded, byte(unit>>12)|0xe0, byte((unit>>6)&0x3f)|0x80, byte(unit&0x3f)|0x80)
		}
	}
	return encoded
}

func frontendDigest(value string) string {
	units := utf16.Encode([]rune(value))
	words := make([]uint32, (len(units)+3)/4)
	for i, unit := range units {
		words[i/4] |= uint32(unit) << uint(24-8*(i%4))
	}
	raw := make([]byte, 4*len(words))
	for i, word := range words {
		binary.BigEndian.PutUint32(raw[i*4:], word)
	}
	sum := sha1.Sum(raw[:len(units)])
	return hex.EncodeToString(sum[:])
}
