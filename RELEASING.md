# Releasing gohc 0.1.0

The root Go module contains the monitor, client and shared report protocol. Tag
`v0.1.0` releases the CLI and `github.com/ramazan-bagaev/gohc/client` together. The
Java artifacts use Maven version `0.1.0`; their shared parent SCM tag is `v0.1.0`.

1. Review changes and repository history before changing GitHub visibility. Keep
   private keys, passphrases and Central credentials outside Git.
2. Run `make test test-java` and the Java reactor verification. Commit the source,
   create `v0.1.0` at that exact commit, then push the commit and tag to GitHub.
3. Run `python3 scripts/release.py --version v0.1.0 --sign` using a supported Go
   toolchain. Verify checksums/signatures and smoke-test a native binary. Prepare a
   GitHub Release with the four CLI archives, signatures, SHA256SUMS and public key.
4. Run `python3 clients/release-local.py --central-bundle` to generate the signed
   Maven bundle without uploading. Publish the parent and both Java modules with
   `python3 clients/release-local.py --publish` after Central credentials are set.
5. When the repository/tag is public, verify Go client download in a fresh module:
   `go get github.com/ramazan-bagaev/gohc/client@v0.1.0`. Verify Java dependencies
   using a fresh Maven repository after Central publication propagates.

Publications are immutable. Never repoint an already published version tag or
replace an existing Maven version. Increment the version for later changes.

The public signing key is on keyserver.ubuntu.com. Local secret storage is
`~/Library/Application Support/gohc/signing/` on this Mac. Only public release
artifacts belong in GitHub assets. Maven Central credentials use server ID
`central` in private `~/.m2/settings.xml`.
