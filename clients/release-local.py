#!/usr/bin/env python3
"""Build signed artifacts; upload only when --publish is explicitly requested."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import xml.etree.ElementTree as ET


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    default = (Path.home() / "Library/Application Support" if sys.platform == "darwin"
               else Path(os.environ.get("XDG_DATA_HOME", Path.home() / ".local/share"))) / "gohc/signing"
    parser.add_argument("--signing-dir", type=Path,
                        default=Path(os.environ.get("GOHC_SIGNING_DIR", str(default))))
    parser.add_argument("--install", action="store_true", help="Also install signed artifacts locally")
    publishing = parser.add_mutually_exclusive_group()
    publishing.add_argument("--central-bundle", action="store_true", help="Prepare a Central bundle without uploading")
    publishing.add_argument("--publish", action="store_true", help="Upload, validate and publish to Maven Central")
    args = parser.parse_args()
    if args.install and (args.central_bundle or args.publish):
        parser.error("--install cannot be combined with a Central deployment mode")
    pom = Path(__file__).with_name("pom.xml").resolve()
    if args.publish:
        repo = pom.parent.parent
        ns = {"m": "http://maven.apache.org/POM/4.0.0"}
        version = ET.parse(pom).getroot().find("m:version", ns).text
        tag = "v" + version
        if subprocess.check_output(["git", "status", "--porcelain"], cwd=repo, text=True).strip():
            parser.error("Commit release changes before publishing")
        head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip()
        tagged = subprocess.check_output(["git", "rev-parse", tag + "^{commit}"], cwd=repo, text=True).strip()
        if head != tagged:
            parser.error("Release tag must point to the current commit")
        settings = Path.home() / ".m2/settings.xml"
        if not settings.is_file():
            parser.error("Configure the Central user token as server 'central' in ~/.m2/settings.xml")
        settings_root = ET.parse(settings).getroot()
        if not any(e.tag.split("}")[-1] == "server" and any(c.tag.split("}")[-1] == "id" and c.text == "central" for c in e) for e in settings_root.iter()):
            parser.error("Maven settings must contain server 'central'")
    directory = args.signing_dir.expanduser().resolve()
    config = json.loads((directory / "signing.json").read_text())
    passphrase = directory / "passphrase"
    if passphrase.stat().st_mode & 0o077:
        parser.error("Signing passphrase must be readable only by its owner (chmod 600)")
    env = os.environ.copy()
    env["GNUPGHOME"] = str(directory / "gnupg")
    env["MAVEN_GPG_PASSPHRASE"] = passphrase.read_text().strip()
    command = ["mvn", "-B", "-f", str(pom),
               "-Prelease,central" if (args.central_bundle or args.publish) else "-Prelease",
               "-Dgpg.keyname=" + config["fingerprint"]]
    if args.central_bundle:
        command.append("-Dgohc.central.skipUpload=true")
    command += ["clean", "deploy" if (args.central_bundle or args.publish) else ("install" if args.install else "verify")]
    subprocess.run(command, env=env, check=True)


if __name__ == "__main__":
    main()
