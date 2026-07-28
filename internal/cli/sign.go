package cli

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/gaarutyunov/epos/internal/sign"
)

// newGenerateKeyPairCommand writes a signing keypair.
//
// It exists because `epos sign` needs a key and cosign's own generate-key-pair
// writes an encrypted private key epos cannot read (internal/sign's package
// doc says why). The public half is a plain PKIX PEM either way, so a cosign
// public key verifies here and an epos public key verifies in cosign.
func newGenerateKeyPairCommand() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "generate-key-pair",
		Short: "Write a signing keypair",
		Long: "generate-key-pair writes cosign.key and cosign.pub. The private key is\n" +
			"an unencrypted PKCS#8 PEM file and is yours to protect; the public key\n" +
			"is the same PKIX PEM cosign writes.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGenerateKeyPair(cmd.OutOrStdout(), dir)
		},
	}
	cmd.Flags().StringVar(&dir, "output-dir", ".", "directory to write the keypair into")
	return cmd
}

func runGenerateKeyPair(out io.Writer, dir string) error {
	key, err := sign.GenerateKey()
	if err != nil {
		return err
	}
	privatePEM, err := sign.MarshalPrivateKey(key)
	if err != nil {
		return err
	}
	publicPEM, err := sign.MarshalPublicKey(&key.PublicKey)
	if err != nil {
		return err
	}

	privatePath := filepath.Join(dir, sign.PrivateKeyFile)
	publicPath := filepath.Join(dir, sign.PublicKeyFile)
	// 0600, and written with O_EXCL: silently replacing a signing key the user
	// still has signatures under is not a recoverable mistake.
	if err := writeNew(privatePath, privatePEM, 0o600); err != nil {
		return err
	}
	if err := writeNew(publicPath, publicPEM, 0o644); err != nil {
		return err
	}

	fmt.Fprintf(out, "%s\n%s\n", privatePath, publicPath)
	return nil
}

func writeNew(path string, body []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	return f.Close()
}

// newSignCommand attaches a cosign signature to a published skill.
func newSignCommand() *cobra.Command {
	var (
		keyPath   string
		plainHTTP bool
	)

	cmd := &cobra.Command{
		Use:   "sign <ref>",
		Short: "Sign a published skill, as an OCI referrer",
		Long: "sign attaches a cosign signature to the skill's manifest, as a referrer\n" +
			"whose subject is that manifest (SPEC.md 11).\n\n" +
			"The signature is written to the registry the skill lives in, which is\n" +
			"the upstream registry rather than epos-registry: epos-registry serves no\n" +
			"write path (SPEC.md 4.5), and a referrer can only live beside what it\n" +
			"refers to.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSign(cmd.Context(), cmd.OutOrStdout(), args[0], keyPath, plainHTTP)
		},
	}
	cmd.Flags().StringVar(&keyPath, "key", sign.PrivateKeyFile, "private key to sign with")
	cmd.Flags().BoolVar(&plainHTTP, "plain-http", false, "talk to the registry over HTTP")
	return cmd
}

func runSign(ctx context.Context, out io.Writer, ref, keyPath string, plainHTTP bool) error {
	repo, tag, err := newRepository(ref, plainHTTP)
	if err != nil {
		return err
	}
	key, err := loadPrivateKey(keyPath)
	if err != nil {
		return err
	}

	subject, err := repo.Resolve(ctx, tag)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", ref, err)
	}

	// The identity the payload records is the repository, without the tag: a
	// tag moves, and what the signature is about is the manifest digest below.
	identity := repo.Reference.Registry + "/" + repo.Reference.Repository
	desc, err := sign.Sign(ctx, repo, identity, subject, key)
	if err != nil {
		return fmt.Errorf("sign %s: %w", ref, err)
	}

	fmt.Fprintf(out, "signature %s %s\n", desc.Digest, subject.Digest)
	return nil
}

// newAttestCommand attaches an in-toto attestation, the same way.
func newAttestCommand() *cobra.Command {
	var (
		keyPath       string
		predicatePath string
		predicateType string
		plainHTTP     bool
	)

	cmd := &cobra.Command{
		Use:   "attest <ref>",
		Short: "Attest a published skill, as an OCI referrer",
		Long: "attest wraps a predicate document in an in-toto statement and a DSSE\n" +
			"envelope, signs it, and attaches it to the skill's manifest as a\n" +
			"referrer — the same layout a signature uses (SPEC.md 11).",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAttest(cmd.Context(), cmd.OutOrStdout(), args[0],
				keyPath, predicatePath, predicateType, plainHTTP)
		},
	}
	cmd.Flags().StringVar(&keyPath, "key", sign.PrivateKeyFile, "private key to sign with")
	cmd.Flags().StringVar(&predicatePath, "predicate", "", "JSON predicate document")
	cmd.Flags().StringVar(&predicateType, "type", sign.DefaultPredicateType,
		"predicate type the document conforms to")
	cmd.Flags().BoolVar(&plainHTTP, "plain-http", false, "talk to the registry over HTTP")
	return cmd
}

func runAttest(ctx context.Context, out io.Writer, ref, keyPath, predicatePath,
	predicateType string, plainHTTP bool) error {
	repo, tag, err := newRepository(ref, plainHTTP)
	if err != nil {
		return err
	}
	key, err := loadPrivateKey(keyPath)
	if err != nil {
		return err
	}

	var predicate json.RawMessage
	if predicatePath != "" {
		body, err := os.ReadFile(predicatePath)
		if err != nil {
			return fmt.Errorf("read %s: %w", predicatePath, err)
		}
		predicate = body
	}

	subject, err := repo.Resolve(ctx, tag)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", ref, err)
	}

	name := repo.Reference.Registry + "/" + repo.Reference.Repository
	desc, err := sign.Attest(ctx, repo, name, subject, predicateType, predicate, key)
	if err != nil {
		return fmt.Errorf("attest %s: %w", ref, err)
	}

	fmt.Fprintf(out, "attestation %s %s\n", desc.Digest, subject.Digest)
	return nil
}

func loadPrivateKey(path string) (*ecdsa.PrivateKey, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	key, err := sign.ParsePrivateKey(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return key, nil
}
