package netutil

import (
	"net"
	"testing"
)

func TestRandomMAC(t *testing.T) {
	mac := RandomMAC()
	hw, err := net.ParseMAC(mac)
	if err != nil {
		t.Fatalf("invalid MAC %q: %v", mac, err)
	}
	if len(hw) != 6 {
		t.Fatalf("MAC length = %d", len(hw))
	}
	if mac[:9] != "52:54:00:" {
		t.Fatalf("MAC prefix = %q", mac[:9])
	}
}

func TestDeterministicMAC(t *testing.T) {
	mac1 := DeterministicMAC("test-seed")
	mac2 := DeterministicMAC("test-seed")
	if mac1 != mac2 {
		t.Fatalf("same seed produced different MACs: %q != %q", mac1, mac2)
	}
	if mac1 == DeterministicMAC("other-seed") {
		t.Fatal("different seeds produced same MAC")
	}
	hw, err := net.ParseMAC(mac1)
	if err != nil {
		t.Fatalf("invalid MAC %q: %v", mac1, err)
	}
	if hw[3]&0x02 != 0x02 {
		t.Fatalf("locally administered bit not set in %q", mac1)
	}
	if hw[3]&0x01 != 0 {
		t.Fatalf("multicast bit set in %q", mac1)
	}
}
