package stick

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"strings"
)

// SHA512Crypt returns a crypt(3) "$6$" hash as /etc/shadow wants it
// (Ulrich Drepper's algorithm, 5000 rounds), so no openssl/mkpasswd is
// needed on the laptop.
func SHA512Crypt(password string) string {
	raw := make([]byte, 12)
	rand.Read(raw)
	salt := strings.NewReplacer("+", ".", "/", "/").Replace(base64.RawStdEncoding.EncodeToString(raw))[:16]
	return sha512CryptSalt(password, salt)
}

func sha512CryptSalt(password, salt string) string {
	const rounds = 5000
	pw, sl := []byte(password), []byte(salt)
	b := sha512.New()
	b.Write(pw)
	b.Write(sl)
	b.Write(pw)
	B := b.Sum(nil)

	a := sha512.New()
	a.Write(pw)
	a.Write(sl)
	for n := len(pw); n > 0; n -= 64 {
		if n > 64 {
			a.Write(B)
		} else {
			a.Write(B[:n])
		}
	}
	for n := len(pw); n > 0; n >>= 1 {
		if n&1 != 0 {
			a.Write(B)
		} else {
			a.Write(pw)
		}
	}
	A := a.Sum(nil)

	dp := sha512.New()
	for range pw {
		dp.Write(pw)
	}
	DP := dp.Sum(nil)
	P := make([]byte, 0, len(pw))
	for n := len(pw); n > 0; n -= 64 {
		if n > 64 {
			P = append(P, DP...)
		} else {
			P = append(P, DP[:n]...)
		}
	}
	ds := sha512.New()
	for i := 0; i < 16+int(A[0]); i++ {
		ds.Write(sl)
	}
	DS := ds.Sum(nil)
	S := make([]byte, 0, len(sl))
	for n := len(sl); n > 0; n -= 64 {
		if n > 64 {
			S = append(S, DS...)
		} else {
			S = append(S, DS[:n]...)
		}
	}

	C := A
	for i := 0; i < rounds; i++ {
		c := sha512.New()
		if i&1 != 0 {
			c.Write(P)
		} else {
			c.Write(C)
		}
		if i%3 != 0 {
			c.Write(S)
		}
		if i%7 != 0 {
			c.Write(P)
		}
		if i&1 != 0 {
			c.Write(C)
		} else {
			c.Write(P)
		}
		C = c.Sum(nil)
	}

	const itoa64 = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var out strings.Builder
	b64 := func(b2, b1, b0 byte, n int) {
		w := uint(b2)<<16 | uint(b1)<<8 | uint(b0)
		for ; n > 0; n-- {
			out.WriteByte(itoa64[w&0x3f])
			w >>= 6
		}
	}
	order := [][3]int{{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45}, {25, 46, 4}, {47, 5, 26}, {6, 27, 48}, {28, 49, 7}, {50, 8, 29}, {9, 30, 51}, {31, 52, 10}, {53, 11, 32}, {12, 33, 54}, {34, 55, 13}, {56, 14, 35}, {15, 36, 57}, {37, 58, 16}, {59, 17, 38}, {18, 39, 60}, {40, 61, 19}, {62, 20, 41}}
	for _, o := range order {
		b64(C[o[0]], C[o[1]], C[o[2]], 4)
	}
	b64(0, 0, C[63], 2)
	return "$6$" + salt + "$" + out.String()
}
