//go:build !darwin

package usage

import "errors"

func readKeychain() ([]byte, error) { return nil, errors.New("no keychain on this OS") }
