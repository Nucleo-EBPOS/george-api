package auth

import (
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// TotpIssuer aparece no app autenticador.
const TotpIssuer = "George"

// NewTotp gera um segredo TOTP e a URL otpauth://.
func NewTotp(account string) (secret, url string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      TotpIssuer,
		AccountName: account,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// ValidateTotp confere o código de 6 dígitos.
func ValidateTotp(secret, code string) bool {
	return totp.Validate(code, secret)
}
