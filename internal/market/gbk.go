package market

import (
	"bytes"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
	"io"
)

// decodeGBK 把 GBK 编码的 bytes 解码成 UTF-8 字符串。
func decodeGBK(b []byte) (string, error) {
	r := transform.NewReader(bytes.NewReader(b), simplifiedchinese.GBK.NewDecoder())
	out, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
