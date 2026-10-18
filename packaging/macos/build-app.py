#!/usr/bin/env python3
"""Wrap an explicitly staged macOS payload in a standard .app (no live data)."""
import argparse
import hashlib
import json
import plistlib
import re
import shutil
import subprocess
import tempfile
from pathlib import Path


def build_app(payload, output, version, icon):
    payload, output, icon = (Path(p).resolve() for p in (payload, output, icon))
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?", version):
        raise ValueError("version must be major.minor.patch with optional development suffix")
    if output.suffix != ".app" or output.exists():
        raise ValueError("output must be a new .app directory; existing output is never replaced")
    if output == payload or payload in output.parents or output in payload.parents:
        raise ValueError("payload and output must be separate trees")
    required = ["meshmux", "meshmux-tray", "bin/mihomo", "bin/tailscale", "bin/tailscaled",
                "bin/geoip.metadb", "dashboard/index.html", "meshmux.example.json"]
    for name in required:
        path = payload / name
        if not path.is_file() or path.is_symlink():
            raise ValueError("missing or linked payload: " + name)
    for name in required[:5]:
        description = subprocess.check_output(["file", "-b", str(payload / name)], text=True)
        if "Mach-O 64-bit executable x86_64" not in description:
            raise ValueError("expected Intel macOS executable: " + name)
        if not (payload / name).stat().st_mode & 0o111:
            raise ValueError("payload is not executable: " + name)
    # Reject links even in the dashboard; never follow a link out of staging.
    for name in ["bin", "dashboard"]:
        if (payload / name).is_symlink() or any(p.is_symlink() for p in (payload / name).rglob("*")):
            raise ValueError("linked resources are not allowed: " + name)
    if not icon.is_file():
        raise ValueError("missing application icon")

    output.parent.mkdir(parents=True, exist_ok=True)
    # Work beside the destination and publish only the complete candidate.
    with tempfile.TemporaryDirectory(prefix=".meshmux-app-", dir=output.parent) as temp:
        app = Path(temp) / output.name
        macos, resources = app / "Contents/MacOS", app / "Contents/Resources"
        macos.mkdir(parents=True)
        resources.mkdir()
        for name in ["meshmux", "meshmux-tray"]:
            shutil.copy2(payload / name, macos / name)
        # An allowlist excludes local config, state, logs and development files.
        (resources / "bin").mkdir()
        for name in ["mihomo", "tailscale", "tailscaled", "geoip.metadb"]:
            shutil.copy2(payload / "bin" / name, resources / "bin" / name)
        shutil.copytree(payload / "dashboard", resources / "dashboard")
        for name in ["meshmux.example.json", "README-macOS.md", "README.md", "README.en.md",
                     "LICENSE", "SECURITY.md", "THIRD_PARTY_NOTICES.md"]:
            if (payload / name).is_file():
                if (payload / name).is_symlink():
                    raise ValueError("linked resource: " + name)
                shutil.copy2(payload / name, resources / name)
        iconset = Path(temp) / "MeshMux.iconset"
        iconset.mkdir()
        for size in [16, 32, 128, 256, 512]:
            for scale in [1, 2]:
                suffix = "@2x" if scale == 2 else ""
                subprocess.run(["sips", "-s", "format", "png", "-z", str(size * scale),
                                str(size * scale), str(icon), "--out",
                                str(iconset / (f"icon_{size}x{size}{suffix}.png"))],
                               check=True, stdout=subprocess.DEVNULL)
        subprocess.run(["iconutil", "-c", "icns", str(iconset), "-o", str(resources / "MeshMux.icns")], check=True)
        info = {
            "CFBundleName": "MeshMux", "CFBundleDisplayName": "MeshMux",
            "CFBundleIdentifier": "com.meshmux.app", "CFBundleExecutable": "meshmux-tray",
            "CFBundlePackageType": "APPL", "CFBundleIconFile": "MeshMux.icns",
            "CFBundleShortVersionString": version.split("-")[0],
            "CFBundleVersion": version.split("-")[0],
            "LSUIElement": True, "LSMinimumSystemVersion": "12.0",
            "NSHighResolutionCapable": True,
            "NSHumanReadableCopyright": "MeshMux contributors — MIT License",
        }
        with (app / "Contents/Info.plist").open("wb") as f:
            plistlib.dump(info, f)
        (app / "Contents/PkgInfo").write_bytes(b"APPL????")
        subprocess.run(["plutil", "-lint", str(app / "Contents/Info.plist")], check=True)
        # Sign executable bytes BEFORE hashing. Do not deep-resign afterward:
        # modifying a nested Mach-O would invalidate its component manifest.
        for binary in [macos / 'meshmux', macos / 'meshmux-tray',
                       resources / 'bin/mihomo', resources / 'bin/tailscale',
                       resources / 'bin/tailscaled']:
            subprocess.run(['codesign', '--force', '--sign', '-', str(binary)], check=True)
        manifest = {name: hashlib.sha256((resources / 'bin' / name).read_bytes()).hexdigest()
                    for name in ['tailscale', 'tailscaled']}
        (resources / 'tailscale-sha256.json').write_text(json.dumps(manifest, indent=2) + '\n')
        subprocess.run(['codesign', '--force', '--sign', '-', str(app)], check=True)
        subprocess.run(['codesign', '--verify', '--deep', '--strict', str(app)], check=True)
        app.rename(output)
    return output


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--payload", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--icon", default=str(Path(__file__).resolve().parents[2] / "assets/meshmux.ico"))
    args = parser.parse_args()
    print(build_app(args.payload, args.output, args.version, args.icon))
