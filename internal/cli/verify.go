package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/gaarutyunov/epos/internal/sign"
)

// newVerifyCommand checks the signatures attached to a published skill.
//
// It adds nothing to the registry. Referrers are listed through
// GET /v2/<name>/referrers/<digest>, which SPEC.md 4.1 has required since A1
// and epos-registry has relayed since A1; the signature payload comes back
// through the ordinary blob GET. Verification against a plain registry and
// verification through epos-registry are the same requests.
func newVerifyCommand() *cobra.Command {
	var (
		keyPath   string
		plainHTTP bool
	)

	cmd := &cobra.Command{
		Use:   "verify <ref>",
		Short: "Verify the cosign signature attached to a skill",
		Long: "verify resolves the skill's manifest, lists its referrers, and checks\n" +
			"the cosign signature over that manifest (SPEC.md 11). Attestations\n" +
			"attached to the same manifest are verified too, and every one of them\n" +
			"must hold.\n\n" +
			"Verification fetches the signature blob from the skill's own\n" +
			"repository, so a registry fronted by epos-registry counts it as an\n" +
			"unverified download of the skill. That inflation is known and\n" +
			"deliberate: telling a signature blob from a content blob needs state,\n" +
			"and epos-registry holds none (SPEC.md 4.4, 5.2).",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVerify(cmd.Context(), cmd.OutOrStdout(), args[0], keyPath, plainHTTP)
		},
	}
	cmd.Flags().StringVar(&keyPath, "key", sign.PublicKeyFile, "public key to verify against")
	cmd.Flags().BoolVar(&plainHTTP, "plain-http", false, "talk to the registry over HTTP")
	return cmd
}

func runVerify(ctx context.Context, out io.Writer, ref, keyPath string, plainHTTP bool) error {
	repo, tag, err := newRepository(ref, plainHTTP)
	if err != nil {
		return err
	}

	body, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", keyPath, err)
	}
	pub, err := sign.ParsePublicKey(body)
	if err != nil {
		return fmt.Errorf("%s: %w", keyPath, err)
	}

	// Deliberately not the Epos-Download client `pull` uses. SPEC.md 5.2 says
	// the signature fetch below lands in the skill's repository as an
	// *unverified* download; sending the header would move it into the
	// verified count instead, which is a worse lie than the one 11 documents.
	subject, err := repo.Resolve(ctx, tag)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", ref, err)
	}

	result, err := sign.Verify(ctx, repo, ref, subject, pub)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "verified %s %s\n", ref, result.Subject.Digest)
	for _, desc := range result.Signatures {
		fmt.Fprintf(out, "signature %s\n", desc.Digest)
	}
	for _, attestation := range result.Attestations {
		fmt.Fprintf(out, "attestation %s %s\n", attestation.Referrer.Digest,
			attestation.PredicateType)
	}
	return nil
}
