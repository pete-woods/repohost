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

// Package sign defines the interface repohost uses to sign repository metadata.
// Implementations wrap a signing key (typically GPG); repohost takes no crypto
// dependency of its own.
package sign

import "context"

// Signer signs repository metadata. The apt publisher uses both methods (to
// write InRelease and Release.gpg); the yum publisher uses only DetachSign (to
// write repomd.xml.asc). A nil Signer means the repository is published
// unsigned.
type Signer interface {
	// ClearSign returns an inline (clearsigned) signature of data.
	ClearSign(ctx context.Context, data []byte) ([]byte, error)
	// DetachSign returns a detached, ASCII-armored signature of data.
	DetachSign(ctx context.Context, data []byte) ([]byte, error)
}
