#!/usr/bin/env python3
import plistlib
import hashlib
import json
import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("build_app", Path(__file__).with_name("build-app.py"))
module = importlib.util.module_from_spec(spec)
sys.modules["build_app"] = module
spec.loader.exec_module(module)
build_app = module.build_app


class BuildAppTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="meshmux-packaging-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.payload = self.root / "payload"
        self.output = self.root / "Applications/MeshMux.app"
        for name in ["meshmux", "meshmux-tray", "bin/mihomo", "bin/tailscale", "bin/tailscaled",
                     "bin/geoip.metadb", "dashboard/index.html", "meshmux.example.json"]:
            path = self.payload / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("fixture")
            path.chmod(0o755)
        self.icon = self.root / "icon.ico"
        self.icon.write_text("icon fixture")
        self.check = patch("build_app.subprocess.check_output", return_value="Mach-O 64-bit executable x86_64")
        self.check.start()
        self.addCleanup(self.check.stop)
        self.run = patch("build_app.subprocess.run")
        self.run.start()
        self.addCleanup(self.run.stop)

    def build(self):
        return build_app(self.payload, self.output, "0.3.9-dev", self.icon)

    def test_bundle_metadata_and_no_runtime_data(self):
        (self.payload / "meshmux.local.json").write_text("private")
        (self.payload / "bin/unexpected-file").write_text("not a component")
        app = self.build()
        with (app / "Contents/Info.plist").open("rb") as f:
            info = plistlib.load(f)
        self.assertEqual(info["CFBundleExecutable"], "meshmux-tray")
        self.assertEqual(info["CFBundleIdentifier"], "com.meshmux.app")
        self.assertTrue(info["LSUIElement"])
        self.assertTrue((app / "Contents/MacOS/meshmux").is_file())
        self.assertTrue((app / "Contents/Resources/bin/tailscaled").is_file())
        manifest = json.loads((app / 'Contents/Resources/tailscale-sha256.json').read_text())
        for name in ['tailscale', 'tailscaled']:
            self.assertEqual(manifest[name], hashlib.sha256(
                (app / 'Contents/Resources/bin' / name).read_bytes()).hexdigest())
        self.assertFalse((app / "Contents/Resources/meshmux.local.json").exists())
        self.assertFalse((app / "Contents/Resources/bin/unexpected-file").exists())

    def test_existing_output_not_overwritten(self):
        self.output.mkdir(parents=True)
        marker = self.output / "keep"
        marker.write_text("keep")
        with self.assertRaises(ValueError):
            self.build()
        self.assertEqual(marker.read_text(), "keep")

    def test_missing_components_fail_before_publication(self):
        for name in ["bin/tailscale", "bin/tailscaled"]:
            with self.subTest(name=name):
                path = self.payload / name
                data = path.read_bytes()
                path.unlink()
                with self.assertRaisesRegex(ValueError, "missing"):
                    self.build()
                self.assertFalse(self.output.exists())
                path.write_bytes(data)
                path.chmod(0o755)

    def test_symlink_rejected(self):
        (self.payload / "dashboard/outside").symlink_to(self.icon)
        with self.assertRaisesRegex(ValueError, "linked"):
            self.build()
        self.assertFalse(self.output.exists())

    def test_wrong_architecture_rejected(self):
        with patch("build_app.subprocess.check_output", return_value="ELF executable"):
            with self.assertRaisesRegex(ValueError, "Intel macOS"):
                self.build()
        self.assertFalse(self.output.exists())

    def test_non_executable_rejected(self):
        (self.payload / "meshmux-tray").chmod(0o644)
        with self.assertRaisesRegex(ValueError, "not executable"):
            self.build()

    def test_payload_cannot_contain_output(self):
        with self.assertRaisesRegex(ValueError, "separate trees"):
            build_app(self.payload, self.payload / "MeshMux.app", "0.3.9", self.icon)


if __name__ == "__main__":
    unittest.main()
