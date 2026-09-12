package id

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func New() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

func Short(n int) string {
	raw := New()
	compact := make([]byte, 0, 32)
	for i := 0; i < len(raw); i++ {
		if raw[i] != '-' {
			compact = append(compact, raw[i])
		}
	}
	if n > len(compact) {
		n = len(compact)
	}
	return string(compact[:n])
}
