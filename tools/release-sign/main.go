// Command release-sign makes and uses the release signing key. The secret key
// only ever travels through stdin and stdout, so it can go straight from and to
// Bitwarden without touching the disk or the screen.
//
//	release-sign genkey -pub FILE      new key: public key to FILE, secret key to stdout
//	release-sign check                 secret key on stdin matches the built-in public key?
//	release-sign sign -comment TEXT F  secret key on stdin; writes F.minisig
//	release-sign verify F              checks F.minisig with the built-in public key
//
// scripts/make-signing-key.sh and scripts/sign-release.sh run it.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	"github.com/ltsjoe/psclassic-launcher/internal/selfupdate"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "release-sign:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: release-sign genkey|check|sign|verify ...")
	}
	switch args[0] {
	case "genkey":
		fs := flag.NewFlagSet("genkey", flag.ContinueOnError)
		pub := fs.String("pub", "", "where to write the public key")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *pub == "" {
			return errors.New("genkey needs -pub FILE")
		}
		if term.IsTerminal(int(os.Stdout.Fd())) {
			return errors.New("refusing to print the secret key to a terminal; pipe it into Bitwarden")
		}
		s, err := selfupdate.GenerateKey()
		if err != nil {
			return err
		}
		if err := os.WriteFile(*pub, []byte(s.Public().String()), 0o644); err != nil {
			return err
		}
		_, err = fmt.Println(s.String())
		return err
	case "check":
		s, err := readSecret()
		if err != nil {
			return err
		}
		k, err := selfupdate.ReleaseKey()
		if err != nil {
			return err
		}
		if s.Public().ID != k.ID || !bytes.Equal(s.Public().Key, k.Key) {
			return fmt.Errorf("the secret key (%s) is not the built-in release key (%s)", s.Public().IDString(), k.IDString())
		}
		fmt.Println("OK: the secret key matches the built-in release key", k.IDString())
		return nil
	case "sign":
		fs := flag.NewFlagSet("sign", flag.ContinueOnError)
		comment := fs.String("comment", "", "trusted comment, e.g. \"psclassic-launcher 0.3.0\"")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 || *comment == "" {
			return errors.New("usage: release-sign sign -comment TEXT FILE")
		}
		s, err := readSecret()
		if err != nil {
			return err
		}
		msg, err := os.ReadFile(fs.Arg(0))
		if err != nil {
			return err
		}
		sig, err := s.Sign(msg, *comment)
		if err != nil {
			return err
		}
		if err := os.WriteFile(fs.Arg(0)+".minisig", sig, 0o644); err != nil {
			return err
		}
		return verify(fs.Arg(0))
	case "verify":
		if len(args) != 2 {
			return errors.New("usage: release-sign verify FILE")
		}
		return verify(args[1])
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func readSecret() (selfupdate.SecretKey, error) {
	b, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
	if err != nil {
		return selfupdate.SecretKey{}, err
	}
	return selfupdate.ParseSecretKey(string(b))
}

func verify(file string) error {
	k, err := selfupdate.ReleaseKey()
	if err != nil {
		return err
	}
	msg, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(file + ".minisig")
	if err != nil {
		return err
	}
	trusted, err := k.Verify(msg, sig)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	fmt.Printf("%s: signature OK (key %s, %q)\n", file, k.IDString(), trusted)
	return nil
}
