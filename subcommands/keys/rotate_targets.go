package keys

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/foundriesio/fioctl/client"
	"github.com/foundriesio/fioctl/subcommands"
)

func init() {
	rotateTargets := &cobra.Command{
		Use:   "rotate-targets <offline-creds.tgz>",
		Short: "Rotate the offline target signing key for the Factory",
		Run:   doRotateTargets,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			api = subcommands.Login(cmd)
		},
		Args: cobra.ExactArgs(1),
	}
	subcommands.RequireFactory(rotateTargets)
	cmd.AddCommand(rotateTargets)
}

func doRotateTargets(cmd *cobra.Command, args []string) {
	factory := viper.GetString("factory")
	credsFile := args[0]
	assertWritable(credsFile)
	creds := getOfflineCreds(credsFile)

	root, err := api.TufRootGet(factory)
	subcommands.DieNotNil(err)

	// Target "rotation" works like this:
	// 1. Find the "online target key" - this the key used by CI, so we don't
	//    want to lose it.
	// 2. Generate a new key.
	// 3. Set these keys in root.json

	onlineTargetId, err := findOnlineTargetId(factory, *root, creds)
	subcommands.DieNotNil(err)

	rootid, rootPk, err := findRoot(*root, creds)
	fmt.Println("= Root keyid:", rootid)
	subcommands.DieNotNil(err)
	targetid, newCreds := replaceOfflineTargetKey(root, onlineTargetId, creds)
	fmt.Println("= New target:", targetid)
	subcommands.DieNotNil(signRoot(root, TufSigner{rootid, rootPk}))

	tmpCreds := saveTempCreds(credsFile, newCreds)

	bytes, err := json.MarshalIndent(root, "", "  ")
	subcommands.DieNotNil(err)
	fmt.Println("= Uploading new root")
	body, err := api.TufRootPost(factory, bytes)
	if err != nil {
		fmt.Println("\nERROR:", err)
		fmt.Println(body)
		os.Exit(1)
	}
	if err := os.Rename(tmpCreds, credsFile); err != nil {
		fmt.Println("\nERROR: Unable to update offline creds file.", err)
		fmt.Println("Temp copy still available at:", tmpCreds)
	}
}

func findOnlineTargetId(factory string, root client.AtsTufRoot, creds OfflineCreds) (string, error) {
	onlinePub, err := api.GetFoundriesTargetsKey(factory)
	subcommands.DieNotNil(err)
	for _, keyid := range root.Signed.Roles["targets"].KeyIDs {
		pub := root.Signed.Keys[keyid].KeyValue.Public
		if pub == onlinePub.KeyValue.Public {
			return keyid, nil
		}
	}
	return "", errors.New("Unable to find online target key for factory")
}

func replaceOfflineTargetKey(root *client.AtsTufRoot, onlineTargetId string, creds OfflineCreds) (string, OfflineCreds) {
	pk, err := rsa.GenerateKey(rand.Reader, 2048)
	subcommands.DieNotNil(err)

	var privBytes []byte = x509.MarshalPKCS1PrivateKey(pk)
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privBytes,
	}

	privBytes, err = json.Marshal(client.AtsKey{
		KeyType:  "RSA",
		KeyValue: client.AtsKeyVal{Private: string(pem.EncodeToMemory(block))},
	})
	subcommands.DieNotNil(err)

	pubBytes, err := x509.MarshalPKIXPublicKey(&pk.PublicKey)
	subcommands.DieNotNil(err)

	block = &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubBytes,
	}
	pubBytes = pem.EncodeToMemory(block)
	id := fmt.Sprintf("%x", sha256.Sum256(pubBytes))
	root.Signed.Keys[id] = client.AtsKey{
		KeyType:  "RSA",
		KeyValue: client.AtsKeyVal{Public: string(pubBytes)},
	}
	root.Signed.Roles["targets"].KeyIDs = []string{onlineTargetId, id}
	root.Signed.Roles["targets"].Threshold = 1
	root.Signed.Version += 1

	pubBytes, err = json.Marshal(root.Signed.Keys[id])
	subcommands.DieNotNil(err)

	base := "tufrepo/keys/fioctl-targets-" + id
	creds[base+".pub"] = pubBytes
	creds[base+".sec"] = privBytes
	return id, creds
}
