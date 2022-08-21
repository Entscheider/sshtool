package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	gssh "github.com/gliderlabs/ssh"
	"github.com/mikesmitty/edkey"
	"golang.org/x/crypto/ssh"
	"log"
	"os"
	"os/user"
)

// GenerateServerKey generates an ed25519 certificate and returns the private key as pem and
// the public key in an authorized_keys supported format.
func GenerateServerKey() ([]byte, []byte, error) {
	// https://gist.github.com/rorycl/d300f3ab942fd79e6cc1f37db0c6260f
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return []byte{}, []byte{}, err
	}

	pemBlock := pem.EncodeToMemory(&pem.Block{
		Type:  "OPENSSH PRIVATE KEY",
		Bytes: edkey.MarshalED25519PrivateKey(priv),
	})

	publicKey, err := ssh.NewPublicKey(pub)
	if err != nil {
		return []byte{}, []byte{}, err
	}

	serializedPublicKey := ssh.MarshalAuthorizedKey(publicKey)

	return pemBlock, pubKeyWithMemo(serializedPublicKey), nil
}

// Adds user and host to an authorized_keys formatted public ssh key
func pubKeyWithMemo(pubKey []byte) []byte {
	u, err := user.Current()
	if err != nil {
		return pubKey
	}
	hostname, err := os.Hostname()
	if err != nil {
		return pubKey
	}

	return append(bytes.TrimRight(pubKey, "\n"), []byte(fmt.Sprintf(" %s@%s\n", u.Username, hostname))...)
}

// Loads a private ssh server key at the file in the config and returns a list of [ssh.Signer] from them
// to sign ssh connections. If the server key are not found at the desired location, this function automatically
// generates one.
func (c *Config) getOrGenerateServerKey() ([]gssh.Signer, error) {
	if len(c.ServerKeyFilename) == 0 {
		return nil, fmt.Errorf("at least one host key is required")
	}
	var result []gssh.Signer
	for _, filename := range c.ServerKeyFilename {
		_, err := os.Stat(c.ServerKeyFilename[0])
		var signer gssh.Signer
		if os.IsNotExist(err) {
			// If no key is found, one pair is generated and saved
			log.Println("No private key found, generating one ...")
			privKey, pubKey, err := GenerateServerKey()
			if err != nil {
				return nil, err
			}
			err = os.WriteFile(c.ServerKeyFilename[0], privKey, 0600)
			if err != nil {
				return nil, err
			}
			err = os.WriteFile(c.ServerKeyFilename[0]+".pub", pubKey, 0600)
			if err != nil {
				return nil, err
			}
			signer, err = ssh.ParsePrivateKey(privKey)
			if err != nil {
				return nil, err
			}
		} else {
			// Otherwise we just read and parse the keys
			data, err := os.ReadFile(filename)
			if err != nil {
				return nil, err
			}
			signer, err = ssh.ParsePrivateKey(data)
			if err != nil {
				return nil, err
			}
		}
		// SSH does not support several keys of the same type (e.g. two ed25519 keys) to be offered to a client.
		// Because of this, an error is returned in this case.
		for _, s := range result {
			if s.PublicKey().Type() == signer.PublicKey().Type() {
				return nil, fmt.Errorf("have two keys with of the same type %s", signer.PublicKey().Type())
			}
		}
		result = append(result, signer)
	}
	return result, nil
}
