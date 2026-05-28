package password

import (
	"fmt"

	"github.com/GehirnInc/crypt/sha512_crypt"
)

func SHA512Crypt(value string) (string, error) {
	hash, err := sha512_crypt.New().Generate([]byte(value), nil)
	if err != nil {
		return "", fmt.Errorf("generate sha512-crypt hash: %w", err)
	}
	return hash, nil
}
