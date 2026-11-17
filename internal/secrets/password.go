// Package secrets generates and validates database credential passwords.
//
// The rules come from Capella's Management API (CreateDatabaseCredentialRequest.password): at
// least 8 characters with an upper-case letter, a lower-case letter, a number and a special
// character, and none of `< > ; . * & | £ =`. It is a port of capellactl's output/password_gen.py
// with `=` also forbidden (Capella rejects it at create time).
package secrets

import (
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// Forbidden lists the characters Capella rejects in a password.
	// Capella also rejects '='; keep it out of Special and reject it in MeetsRules.
	Forbidden = "<>;.*&|£="
	// Special is the alphabet of special characters used by generated passwords. It contains no
	// Forbidden character.
	Special = "!@#$%^()-_+[]{}:,?"
	// Rules is the human-readable password policy, for error messages.
	Rules = "min 8, upper, lower, digit, special, none of < > ; . * & | £ ="

	upper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	lower = "abcdefghijklmnopqrstuvwxyz"
	digit = "0123456789"

	// MinLength is the shortest password Capella accepts. GeneratePassword never returns less.
	MinLength = 8
)

// classes holds one alphabet per required character class.
var classes = [...]string{upper, lower, digit, Special}

// MeetsRules reports whether password satisfies Capella's password policy.
func MeetsRules(password string) bool {
	if utf8.RuneCountInString(password) < MinLength {
		return false
	}
	if strings.ContainsAny(password, Forbidden) {
		return false
	}
	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case !unicode.IsLetter(r):
			hasSpecial = true
		}
	}
	return hasUpper && hasLower && hasDigit && hasSpecial
}

// Generate returns a random password of length characters that satisfies MeetsRules: one
// character from each class, the rest from all classes, shuffled with a CSPRNG. A length below
// MinLength is raised to MinLength so the result is always valid (the Python original returned
// 4 characters for lengths up to 4, which Capella would reject). A failure of the system CSPRNG
// is returned as an error; there is no safe fallback.
func Generate(length int) (string, error) {
	if length < MinLength {
		length = MinLength
	}
	alphabet := upper + lower + digit + Special
	chars := make([]byte, 0, length)
	for _, class := range classes {
		i, err := randBelow(len(class))
		if err != nil {
			return "", err
		}
		chars = append(chars, class[i])
	}
	for len(chars) < length {
		i, err := randBelow(len(alphabet))
		if err != nil {
			return "", err
		}
		chars = append(chars, alphabet[i])
	}
	for i := len(chars) - 1; i > 0; i-- {
		j, err := randBelow(i + 1)
		if err != nil {
			return "", err
		}
		chars[i], chars[j] = chars[j], chars[i]
	}
	return string(chars), nil
}

// MustGenerate is Generate for test doubles and tests, where a CSPRNG failure can only be an
// environment fault. Product code uses Generate and returns the error.
func MustGenerate(length int) string {
	pw, err := Generate(length)
	if err != nil {
		panic(err)
	}
	return pw
}

// randReader is the entropy source; tests replace it to exercise the failure path.
var randReader io.Reader = rand.Reader

// randBelow returns a uniform random int in [0, n).
func randBelow(n int) (int, error) {
	v, err := rand.Int(randReader, big.NewInt(int64(n)))
	if err != nil {
		return 0, fmt.Errorf("generating password: %w", err)
	}
	return int(v.Int64()), nil
}
