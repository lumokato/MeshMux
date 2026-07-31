# MeshMux Mihomo build

MeshMux bundles a patched Mihomo core based on upstream `v1.19.26` commit `fc8c5a24b16991f98cd736950c17d1aa306a5041`.

The patch in `tailnet-inbound-forwards.patch` adds generic Tailnet-only TCP and UDP inbound port forwarding to `type: tailscale` proxies. It does not add another authentication layer; Tailscale ACLs and Grants remain authoritative.

Build and verification:

```powershell
pwsh -NoProfile -File scripts/build-mihomo.ps1 -PackageSource
```

The script verifies the upstream commit, applies the patch, runs the full Mihomo test suite with `with_gvisor`, builds the Windows `amd64-v1` core, and creates both binary and corresponding-source ZIP files.

When updating Mihomo:

1. Change the pinned commit and version in `scripts/build-mihomo.ps1`.
2. Rebase or regenerate `tailnet-inbound-forwards.patch` against that commit.
3. Run the full Mihomo tests and MeshMux tests.
4. Update `DefaultMihomoAssetPattern` in `internal/config/config.go`.
5. Publish the binary and corresponding-source archives with the MeshMux release.

This capability is suitable for upstreaming because it is protocol-agnostic, Tailnet-only, disabled by default, and reuses tsnet's existing ACL/Grant enforcement. Until upstream accepts an equivalent lifecycle API, MeshMux keeps the patch against a pinned commit instead of maintaining a permanently diverged source fork.
