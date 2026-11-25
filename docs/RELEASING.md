# Releasing

This page is for the maintainer. A release is made by pushing a version tag. GitHub then builds
everything by itself (`.github/workflows/release.yml`). Nobody builds or signs anything on a laptop.

## What one release contains

- The program for 5 systems: Linux (Intel/AMD and ARM), macOS (Intel and Apple chip), Windows (Intel/AMD).
  Each is one file in an archive, with the README, LICENSE and NOTICE.
- `checksums.txt`, a signed copy of it (`checksums.txt.sigstore.json`), and a build-provenance
  attestation. Together they let anyone check a download. How: "Verify a download" in the README.
- A software bill of materials (`*.sbom.json`) for each archive.
- A container image, `ghcr.io/vivekj21/log-analyzer`, for Linux Intel/AMD and ARM, also signed and attested.

Every pull request already builds these 5 programs (job `release-check` in `ci.yml`), without
publishing anything. The release workflow refuses a tag that is not a version, or whose commit is not on `main`.

## Rehearse first

**What it is.** A rehearsal is a real release with a throwaway name, `v0.1.0-rc1` ("rc" means release
candidate). It is published as a pre-release, so it is not shown as the latest version.

**Why.** The release workflow only runs on a tag, so it cannot be tested any other way. Signing,
attestations and the image push need GitHub's real servers. A mistake found on the real `v0.1.0` is public
at once. A mistake found on `v0.1.0-rc1` costs nothing.

**Who deletes it.** You do, the repository owner, using the steps in "Clean up" below. Nobody else has
the rights, and nothing deletes it by itself.

1. Make sure `main` is green (CI, and the e2e run).
2. Run in the repository folder:
   ```bash
   git switch main && git pull --ff-only
   git tag -a v0.1.0-rc1 -m "Rehearsal. Will be deleted."
   git push origin v0.1.0-rc1
   ```
3. On GitHub open Actions, then the `release` run. Its 3 jobs (`verify`, `binaries`, `image`) must be green.
4. Check the result:
   - The Releases page shows `v0.1.0-rc1` as a pre-release with 5 archives, `checksums.txt`,
     `checksums.txt.sigstore.json` and 5 `.sbom.json` files.
   - Download the archive for your system and do the 3 checks from "Verify a download" in the README.
   - Unpack it and run `log-analyzer --version`. It must say `0.1.0-rc1` and a commit.
   - Image: `docker run --rm ghcr.io/vivekj21/log-analyzer:0.1.0-rc1 --version`. A new image package is
     private at first, so run `docker login ghcr.io` first (user name: your GitHub name; password: a token with `read:packages`).
5. If something failed, fix it on `main`, clean up (below), and tag `v0.1.0-rc2`.

## Clean up after a rehearsal

Do all three. Replace the tag name if you used another.

1. **The release and the tag.** Releases page, open `v0.1.0-rc1`, Delete. Then remove the tag:
   ```bash
   git push origin --delete v0.1.0-rc1
   git tag -d v0.1.0-rc1
   ```
   (With the GitHub CLI, one command does the release and the remote tag: `gh release delete v0.1.0-rc1 --cleanup-tag --yes`.)
2. **The image.** On GitHub open your profile, Packages, `log-analyzer`. If the only versions are from
   rehearsals, delete the whole package (Package settings, Danger Zone). Otherwise delete the rehearsal
   versions: the one tagged `0.1.0-rc1` and the untagged ones made at the same time (they hold the
   per-system images, the signature and the attestation).
3. Nothing else needs removing. The attestation record and the public Sigstore log entry stay. They are
   harmless and cannot be deleted.

## The real release

1. Rehearsal done and cleaned up. `main` is green.
2. Remove the "first release is being prepared" status note from the README, in the same pull request or commit that you tag.
3. Tag and push:
   ```bash
   git switch main && git pull --ff-only
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```
4. First release only: make the image public. Profile, Packages, `log-analyzer`, Package settings,
   Change visibility, Public. Also connect the package to the repository so it shows on the repository page.
5. Do the checks from the rehearsal once more on the real files.

Use the next number for every release: `v0.1.1` for fixes, `v0.2.0` for new features. Never reuse or move a
published tag. If a real release is wrong, publish the next version.

## Keep it healthy

- The Go version used to build is the `toolchain` line in `go.mod`. When the `vuln` job reports a
  problem in the standard library, raise that line to the newest patch release and release again.
- Dependabot opens pull requests for the Go libraries, the GitHub Actions (pinned by commit, with the
  version in a comment) and the Docker base images. Merge them when their checks are green.
- The release tools (GoReleaser, cosign, syft) are pinned to exact versions in `release.yml`, in
  `ci.yml` and in `.goreleaser.yaml`. Raise them on purpose, and only to a version at least two weeks old.
