// Copyright 2026 Pete Steyert-Woods
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

// Package pgp provides an OpenPGP-backed implementation of the signer that the
// apt and yum publishers use to sign repository metadata. It is a separate,
// opt-in package so the core library carries no cryptography dependency: only
// programs that import pgp pull in the OpenPGP implementation.
package pgp

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"

	"github.com/pete-woods/repohost/internal/sign"
)

// Signer signs repository metadata with an OpenPGP private key. It satisfies the
// signer interface accepted by repohost.APTConfig and repohost.YUMConfig.
type Signer struct {
	entity *openpgp.Entity
}

var _ sign.Signer = (*Signer)(nil)

// NewSigner builds a Signer from an ASCII-armored private key. passphrase may be
// empty for an unprotected key; otherwise it is used to decrypt the key material.
func NewSigner(armoredPrivateKey []byte, passphrase string) (*Signer, error) {
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(armoredPrivateKey))
	if err != nil {
		return nil, fmt.Errorf("pgp: reading armored private key: %w", err)
	}
	if len(entities) != 1 {
		return nil, fmt.Errorf("pgp: expected exactly one key in the armored input, got %d", len(entities))
	}

	entity := entities[0]
	if entity.PrivateKey == nil {
		return nil, errors.New("pgp: armored input contains no private key material")
	}
	if passphrase != "" {
		if err := entity.DecryptPrivateKeys([]byte(passphrase)); err != nil {
			return nil, fmt.Errorf("pgp: decrypting private key: %w", err)
		}
	}

	return &Signer{entity: entity}, nil
}

// ClearSign returns an inline (clearsigned) signature of data, used for apt's
// InRelease file.
func (s *Signer) ClearSign(_ context.Context, data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := clearsign.Encode(&buf, s.entity.PrivateKey, nil)
	if err != nil {
		return nil, fmt.Errorf("pgp: clear-signing: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return nil, fmt.Errorf("pgp: clear-signing: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("pgp: clear-signing: %w", err)
	}
	return buf.Bytes(), nil
}

// DetachSign returns a detached, ASCII-armored signature of data, used for apt's
// Release.gpg and yum's repomd.xml.asc.
func (s *Signer) DetachSign(_ context.Context, data []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&buf, s.entity, bytes.NewReader(data), nil); err != nil {
		return nil, fmt.Errorf("pgp: detached-signing: %w", err)
	}
	return buf.Bytes(), nil
}

// ArmoredPublicKey returns the signer's ASCII-armored public key, which clients
// import to verify the repository (apt signed-by, dnf gpgkey).
func (s *Signer) ArmoredPublicKey() ([]byte, error) {
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return nil, fmt.Errorf("pgp: armoring public key: %w", err)
	}
	if err := s.entity.Serialize(w); err != nil {
		return nil, fmt.Errorf("pgp: serializing public key: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("pgp: armoring public key: %w", err)
	}
	return buf.Bytes(), nil
}
