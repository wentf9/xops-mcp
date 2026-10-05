// Package naming defines the server product's resource-name and alias rules.
package naming

import (
	"errors"
	"unicode"
	"unicode/utf8"
)

const MaxBytes = 256

var ErrInvalid = errors.New("names and aliases must be 1-256 UTF-8 bytes using Unicode letters, decimal digits, underscore, hyphen, or combining marks after letters; whitespace, punctuation, symbols and invisible characters are not allowed")

func Validate(value string) error {
	if value == "" || len(value) > MaxBytes || !utf8.ValidString(value) {
		return ErrInvalid
	}
	letter := false
	for _, r := range value {
		if unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) || unicode.Is(unicode.Variation_Selector, r) {
			return ErrInvalid
		}
		switch {
		case unicode.IsLetter(r):
			letter = true
		case unicode.IsDigit(r), r == '_', r == '-':
			letter = false
		case letter && (unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r)):
			// Combining marks belong to the preceding letter, including scripts
			// whose ordinary words require multiple vowel/accent marks.
		default:
			return ErrInvalid
		}
	}
	return nil
}
