package secret

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"k8s.io/klog"
	"strings"
)

const (
	AESKey = "ffffffffffffffff"
)

var (
	ivspec = []byte("0000000000000000")
)

// md5验证
func MD5Str(src string) string {
	h := md5.New()
	h.Write([]byte(src)) // 需要加密的字符串为
	// fmt.Printf("%s\n", hex.EncodeToString(h.Sum(nil))) // 输出加密结果
	return hex.EncodeToString(h.Sum(nil))
}

// hmacsha256验证
func HmacSha256(src, key string) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(src))
	return hex.EncodeToString(m.Sum(nil))
}

// hmacsha512验证
func HmacSha512(src, key string) string {
	m := hmac.New(sha512.New, []byte(key))
	m.Write([]byte(src))
	return hex.EncodeToString(m.Sum(nil))
}

func HmacSha1(src, key string) string {
	m := hmac.New(sha1.New, []byte(key))
	m.Write([]byte(src))
	return hex.EncodeToString(m.Sum(nil))
}

// sha256验证
func SHA256Str(src string) string {
	h := sha256.New()
	h.Write([]byte(src)) // 需要加密的字符串为
	// fmt.Printf("%s\n", hex.EncodeToString(h.Sum(nil))) // 输出加密结果
	return hex.EncodeToString(h.Sum(nil))
}

// sha512验证
func SHA512Str(src string) string {
	h := sha512.New()
	h.Write([]byte(src)) // 需要加密的字符串为
	// fmt.Printf("%s\n", hex.EncodeToString(h.Sum(nil))) // 输出加密结果
	return hex.EncodeToString(h.Sum(nil))
}

// base编码
func BASE64EncodeStr(src string) string {
	return string(base64.StdEncoding.EncodeToString([]byte(src)))
}

// base解码
func BASE64DecodeStr(src string) string {
	a, err := base64.StdEncoding.DecodeString(src)
	if err != nil {
		return ""
	}
	return string(a)
}

func AESEncodeStr(src, key string) (string, error) {

	if src == "" {
		klog.Warning("AESEncodeStr src is blank")
	}

	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		klog.Errorf("AESEncodeStr key error: %v", err)
		return "", err
	}

	ecb := cipher.NewCBCEncrypter(block, ivspec)
	content := []byte(src)
	content = PKCS5Padding(content, block.BlockSize())
	crypted := make([]byte, len(content))
	ecb.CryptBlocks(crypted, content)
	return hex.EncodeToString(crypted), nil
}

func AESDecodeStr(crypt, key string) (string, error) {
	crypted, err := hex.DecodeString(strings.ToLower(crypt))
	if err != nil || len(crypted) == 0 {
		err = fmt.Errorf("AESDecodeStr plain content empty: %v", err)
		klog.Error(err.Error())
		return "", err
	}

	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		err = fmt.Errorf("AESDecodeStr key error: %v", err)
		klog.Error(err.Error())
		return "", err
	}

	ecb := cipher.NewCBCDecrypter(block, ivspec)
	decrypted := make([]byte, len(crypted))
	ecb.CryptBlocks(decrypted, crypted)

	return string(PKCS5Trimming(decrypted)), nil
}

func PKCS5Padding(ciphertext []byte, blockSize int) []byte {
	padding := blockSize - len(ciphertext)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(ciphertext, padtext...)
}

func PKCS5Trimming(encrypt []byte) []byte {
	padding := encrypt[len(encrypt)-1]
	return encrypt[:len(encrypt)-int(padding)]
}

func RsaEncrypt(src, key string) string {
	block, _ := pem.Decode([]byte(key))
	if block == nil {
		return ""
	}

	pubInterface, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		klog.Error(err.Error())
		return ""
	}

	pub := pubInterface.(*rsa.PublicKey)

	crypted, err := rsa.EncryptPKCS1v15(rand.Reader, pub, []byte(src))
	if err != nil {
		klog.Error(err.Error())
		return ""
	}

	return hex.EncodeToString(crypted)
}
