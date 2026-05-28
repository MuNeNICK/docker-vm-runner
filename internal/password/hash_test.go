package password

import (
	"strings"
	"testing"

	"github.com/GehirnInc/crypt/sha512_crypt"
)

func TestSHA512Crypt(t *testing.T) {
	hash, err := SHA512Crypt("secret")
	if err != nil {
		t.Fatalf("SHA512Crypt returned error: %v", err)
	}
	if !strings.HasPrefix(hash, "$6$") {
		t.Fatalf("hash = %q", hash)
	}
	if err := sha512_crypt.New().Verify(hash, []byte("secret")); err != nil {
		t.Fatalf("hash verification failed: %v", err)
	}
}
