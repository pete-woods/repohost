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

package pgp_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/pete-woods/repohost/pgp"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// releaseData stands in for a Release or repomd.xml document to be signed.
var releaseData = []byte("Origin: repohost\nSuite: stable\nComponents: main\n")

func TestDetachSignVerifies(t *testing.T) {
	signer, keyring := newSigner(t)

	sig, err := signer.DetachSign(context.Background(), releaseData)
	assert.NilError(t, err)
	assert.Check(t, cmp.Contains(string(sig), "-----BEGIN PGP SIGNATURE-----"))

	_, err = openpgp.CheckArmoredDetachedSignature(keyring, bytes.NewReader(releaseData), bytes.NewReader(sig), nil)
	assert.NilError(t, err) // nil means the detached signature verified
}

func TestClearSignVerifies(t *testing.T) {
	signer, keyring := newSigner(t)

	signed, err := signer.ClearSign(context.Background(), releaseData)
	assert.NilError(t, err)
	assert.Check(t, cmp.Contains(string(signed), "-----BEGIN PGP SIGNED MESSAGE-----"))
	assert.Check(t, cmp.Contains(string(signed), "Origin: repohost"), "clearsigned output must embed the message")

	block, _ := clearsign.Decode(signed)
	assert.Assert(t, block != nil, "output must be a clearsigned block")

	_, err = openpgp.CheckDetachedSignature(keyring, bytes.NewReader(block.Bytes), block.ArmoredSignature.Body, nil)
	assert.NilError(t, err) // the embedded signature verifies against the public key
}

func TestVerifyFailsWithWrongKey(t *testing.T) {
	signer, _ := newSigner(t)
	_, otherKeyring := newSigner(t)

	sig, err := signer.DetachSign(context.Background(), releaseData)
	assert.NilError(t, err)

	_, err = openpgp.CheckArmoredDetachedSignature(otherKeyring, bytes.NewReader(releaseData), bytes.NewReader(sig), nil)
	assert.Check(t, err != nil, "verification against an unrelated key must fail")
}

func TestNewSignerRejectsNonKey(t *testing.T) {
	_, err := pgp.NewSigner([]byte("not a pgp key"), "")
	assert.Check(t, err != nil)
}

// newSigner generates a throwaway key and returns a Signer built from its
// armored private key, plus a keyring holding the matching public key.
func newSigner(t *testing.T) (*pgp.Signer, openpgp.EntityList) {
	t.Helper()

	entity, err := openpgp.NewEntity("repohost test", "", "test@example.com", nil)
	assert.NilError(t, err)

	var priv bytes.Buffer
	w, err := armor.Encode(&priv, openpgp.PrivateKeyType, nil)
	assert.NilError(t, err)
	err = entity.SerializePrivate(w, nil)
	assert.NilError(t, err)
	err = w.Close()
	assert.NilError(t, err)

	signer, err := pgp.NewSigner(priv.Bytes(), "")
	assert.NilError(t, err)

	pub, err := signer.ArmoredPublicKey()
	assert.NilError(t, err)
	keyring, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(pub))
	assert.NilError(t, err)

	return signer, keyring
}
