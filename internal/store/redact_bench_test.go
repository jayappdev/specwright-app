package store

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkRedactLargeOutput(b *testing.B) {
	r := NewRedactor()
	for i := 0; i < 10; i++ {
		r.Add(fmt.Sprintf("secret-value-%d-abcdefghijklmnop", i))
	}
	s := strings.Repeat("some ordinary build output line with numbers 12345\n", 20000) // ~1 MB
	b.SetBytes(int64(len(s)))
	for i := 0; i < b.N; i++ {
		r.String(s)
	}
}

func BenchmarkRedactLargeOutputNoSecrets(b *testing.B) {
	r := NewRedactor()
	s := strings.Repeat("some ordinary build output line with numbers 12345\n", 20000)
	b.SetBytes(int64(len(s)))
	for i := 0; i < b.N; i++ {
		r.String(s)
	}
}
