# Terraform Provider Release & Publishing Guide

This guide details the step-by-step process for preparing, releasing, and publishing the Atatus Terraform Provider to the official [HashiCorp Terraform Registry](https://registry.terraform.io/).

---

## Step 1: Prepare the GitHub Repository

> [!NOTE]
> **One-time setup, already done (v1.0.0 is on the Registry).** Skip this step for later releases; it is kept for a fresh setup.

1. **Confirm GitHub Organization & Registry Namespace:** Confirm the company GitHub organization and the intended Registry namespace (`atatus`).
2. **Rename Repository:** Rename the repository from `atatus-terraform-provider` to `terraform-provider-atatus`.
   > [!NOTE]
   > The public Terraform Registry requires a public GitHub repository named in the standard format: `terraform-provider-<name>`.
3. **Make Repository Public:** Make the repository public after company approval.
4. **Verify Documentation & Code:** Confirm that `README.md`, `docs/index.md`, resource documentation under `docs/resources/`, examples, and source code are ready.
5. **Verify Provider Address:** Verify that the provider address in `main.go` is:
   ```go
   registry.terraform.io/atatus/atatus
   ```
6. **Confirm Go Module Path:** Confirm the Go module path and imports in `go.mod` and source code are consistent with the final repository location (`github.com/atatus/terraform-provider-atatus`).

---

## Step 2: Commit and Push the Release Configuration

1. **Verify Release Files:** Ensure these files are present in the repository root:
   * `.goreleaser.yml`
   * `terraform-registry-manifest.json`
   * `main.go` with the configurable provider version
   * `.github/workflows/release.yml`
2. **Run Quality & Verification Checks:**
   ```bash
   test -z "$(gofmt -l .)"
   go mod tidy
   go vet ./...
   go test ./...
   TF_ACC=1 go test ./... -count=1
   go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@v0.20.1 validate --provider-name atatus
   go build -o /tmp/terraform-provider-atatus .
   git diff --check
   ```
   The same checks run on every push and pull request (`.github/workflows/test.yml`); the tag should only go on a commit where that workflow passed.
3. **Commit & Push:** Review the changes, commit them, and push them to the approved branch.

---

## Step 3: Configure GPG Signing

> [!NOTE]
> **One-time setup, already done (v1.0.0 is on the Registry).** Skip this step for later releases; it is kept for a fresh setup.

1. **Generate GPG Key:** Generate an RSA GPG signing key:
   ```bash
   gpg --full-generate-key
   ```
2. **Export Public Key:** Export the public key:
   ```bash
   gpg --armor --export "KEY_ID_OR_EMAIL"
   ```
3. **Register on Terraform Registry:** Sign in to the [Terraform Registry](https://registry.terraform.io/) and register the public key under **User Settings → Signing Keys** for the intended publisher (`atatus`).
4. **Export Private Key:** Export the private key securely:
   ```bash
   gpg --armor --export-secret-keys "KEY_ID_OR_EMAIL"
   ```
5. **Configure GitHub Secrets:**
   * Add the private key as the GitHub Actions secret: `GPG_PRIVATE_KEY`
   * Add the key passphrase as the GitHub Actions secret: `PASSPHRASE`

> [!CAUTION]
> The private key and passphrase must remain strictly confidential and must never be committed to Git.

---

## Step 4: Verify the Release Automation

1. **Check GoReleaser Config:** Verify the GoReleaser project name in `.goreleaser.yml` is `terraform-provider-atatus`.
2. **Protocol Version:** Confirm the Registry manifest (`terraform-registry-manifest.json`) contains protocol version `6.0`, consistent with the provider's Plugin Framework configuration.
3. **Workflow Trigger:** Verify that `.github/workflows/release.yml` triggers when a version tag beginning with `v` is pushed (`v*`).
4. **Permissions:** Confirm repository and organization settings allow GitHub Actions workflows to run with the required write permissions (`contents: write`).
5. **Signing Steps:** Ensure the GPG key import and checksum-signing steps in GoReleaser are properly configured.

---

## Step 5: Create the GitHub Release

Once the configuration, signing setup, and repository are ready:

```bash
git switch main
git pull --ff-only origin main
git tag v1.1.0
git push origin v1.1.0
```

> [!IMPORTANT]
> Use `v1.1.0` only if it is the approved release version and the tag does not already exist. `v1.0.0` is already published.

Pushing the tag triggers the GitHub Actions workflow, which runs GoReleaser to compile cross-platform binaries and sign the release artifacts.

Alternatively, run the **Release** workflow manually (Actions → Release → Run workflow, `workflow_dispatch`) and enter the version, e.g. `v1.1.0`. The workflow creates the tag from the selected branch and then releases it the same way.

---

## Step 6: Verify the GitHub Release

1. Open the repository's **Releases** page on GitHub and confirm that the release workflow succeeded.
2. Check that the release contains:
   * Provider ZIP binaries for supported operating systems and architectures (`darwin_amd64`, `darwin_arm64`, `linux_amd64`, `linux_arm64`, `windows_amd64`, etc.).
   * `terraform-provider-atatus_1.1.0_SHA256SUMS`
   * `terraform-provider-atatus_1.1.0_SHA256SUMS.sig`
   * The versioned Registry manifest: `terraform-provider-atatus_1.1.0_manifest.json`
3. Ensure the checksums and signature are valid, and verify the release is published (not saved as a private draft).

---

## Step 7: Publish the Provider in Terraform Registry

> [!NOTE]
> **One-time setup, already done (v1.0.0 is on the Registry).** Skip this step for later releases; it is kept for a fresh setup.

1. Open [https://registry.terraform.io/](https://registry.terraform.io/).
2. Sign in using the GitHub account with the required repository permissions.
3. Select **Publish → Provider**.
4. Select the company namespace: `atatus`.
5. Select the repository: `terraform-provider-atatus`.
6. Choose the appropriate provider category (e.g. *Monitoring & Observability*).
7. Review and accept the Registry terms.
8. Verify all details carefully and publish the provider.

> [!WARNING]
> Publishing to the Registry is permanent, so the namespace (`atatus`) and repository (`terraform-provider-atatus`) must be confirmed before the final action.

---

## Step 8: Verify Installation

Create a separate, empty test project directory with a `main.tf` file:

```hcl
terraform {
  required_providers {
    atatus = {
      source  = "atatus/atatus"
      version = "1.1.0"
    }
  }
}
```

Then run:

```bash
terraform init
terraform validate
```

Confirm that Terraform successfully downloads the provider from the registry and validates the configuration.

---

## References

* [Official HashiCorp Publishing Documentation](https://developer.hashicorp.com/terraform/registry/providers/publishing)
