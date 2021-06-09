package secret

import "testing"

func TestHMAC_SHA1(t *testing.T) {
	secret := HmacSha1("123456", "test")
	t.Log(secret)
}

func TestHMAC_SHA256(t *testing.T) {
	secret := HmacSha256("123456", "test")
	t.Log(secret)
}

func TestHMAC_SHA512(t *testing.T) {
	secret := HmacSha512("123456", "test")
	t.Log(secret)
}

func TestAESDecodeStr(t *testing.T) {

}

func TestAESEncodeStr(t *testing.T) {
	//key := "123456"
	secret, err := AESEncodeStr("123456", AESKey)
	if err != nil {
		t.Fatalf(err.Error())
	}

	t.Logf("Encode result is %v", secret)

	deSecret, err := AESDecodeStr(secret, AESKey)
	if err != nil {
		t.Fatalf(err.Error())
	}

	t.Logf("DEncode result is %v", deSecret)
}

func TestAESDecodeStr2(t *testing.T) {
	pwd := "c38094aece22e47ae36389b055eb6b05"
	secret, err := AESDecodeStr(pwd, AESKey)
	if err != nil {
		t.Fatalf(err.Error())
	}

	t.Logf("Encode result is %v", secret)
}

func TestMD5Str(t *testing.T) {

}

func TestPKCS5Trimming(t *testing.T) {

}
