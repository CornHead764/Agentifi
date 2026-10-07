package domain

import "strings"

// LastFour is the last four digits of an account or card number, every digit
// in it joined first: "5555512-34" is "1234" wherever it is read, so a mailed
// bill and a pulled subaccount agree on the same number however each writes
// it. Fewer than four digits is "".
func LastFour(number string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, number)
	if len(digits) < 4 {
		return ""
	}
	return digits[len(digits)-4:]
}

// MaskAccount is an account number as the screens show it, "••••1234", or ""
// when it has fewer than four digits.
func MaskAccount(number string) string {
	four := LastFour(number)
	if four == "" {
		return ""
	}
	return "••••" + four
}
