package tools

import "encoding/base64"

// Base64Encode converts a byte slice to a Base64 string
func Base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// Base64Decode converts a Base64 string back to a byte slice
func Base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
