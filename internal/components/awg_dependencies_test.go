package components

import (
	"errors"
	"testing"
)

func TestAmneziaWGRequiresAllExecutableDependencies(t *testing.T) {
	all := map[string]bool{"amneziawg-go": true, "awg": true, "awg-quick": true, "ip": true, "bash": true}
	lookup := func(name string) (string, error) {
		if all[name] {
			return "/tmp/" + name, nil
		}
		return "", errors.New("absent")
	}
	if !awgToolsAvailable(lookup) {
		t.Fatal("complete AWG runtime was not accepted")
	}
	delete(all, "awg-quick")
	if awgToolsAvailable(lookup) {
		t.Fatal("AWG runtime falsely advertised installed without awg-quick")
	}
}
