package tools

import "encoding/base64"

// Base64Encode 將位元組陣列轉換為 Base64 字串
func Base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// Base64Decode 將 Base64 字串轉換回位元組陣列
func Base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
