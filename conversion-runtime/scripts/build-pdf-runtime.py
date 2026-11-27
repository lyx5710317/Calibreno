"""Build the self-contained Windows PDF and OCR runtime for Electron Forge."""

import hashlib
from importlib import metadata
import os
from pathlib import Path
import shutil
import struct
import subprocess
import sys


SCRIPTS = Path(__file__).resolve().parent
ROOT = SCRIPTS.parent / ".local" / "pdf-runtime"
REQUIREMENTS = SCRIPTS / "pdf-runtime-requirements.txt"
WORKER = SCRIPTS / "pdf-ocr-worker.py"
DIST = ROOT / "dist"
EXE = DIST / "pdf-ocr" / "pdf-ocr.exe"
PYTHON_KEY = hashlib.sha256(f"{sys.base_prefix}\n{sys.version}".encode()).hexdigest()[:16]


def run(*arguments):
    subprocess.run([str(argument) for argument in arguments], check=True)


def copy_licenses():
    dest = DIST / "pdf-ocr" / "licenses"
    for package in metadata.distributions():
        name = package.metadata["Name"]
        for file in package.files or ():
            parts = file.parts
            is_package = parts[0].lower() in {name.lower().replace("-", "_"), name.lower(), "cv2"}
            if len(parts) < 2 or not (is_package or parts[0].endswith(".dist-info")):
                continue
            if not parts[1].upper().startswith(("LICENSE", "COPYING", "NOTICE", "THIRD_PARTY")):
                continue
            source = file.locate()
            if source.is_file():
                target = dest.joinpath(name, *parts[1:])
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(source, target)
    base = Path(sys.base_prefix)
    license_file = next((file for file in (base / "LICENSE.txt", base / "LICENSE_PYTHON.txt") if file.is_file()), None)
    if license_file is None:
        raise RuntimeError("Could not locate the build interpreter's Python license.")
    dest.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(license_file, dest / "Python.txt")


def freeze():
    run(sys.executable, "-m", "PyInstaller", "--noconfirm", "--onedir",
        "--name", "pdf-ocr", "--distpath", DIST, "--workpath", ROOT / "build" / PYTHON_KEY,
        "--specpath", ROOT, "--collect-data", "rapidocr", "--collect-all", "pypdfium2_raw",
        "--hidden-import", "rapidocr.inference_engine.onnxruntime", "--collect-binaries", "onnxruntime",
        "--exclude-module", "pytest", WORKER)
    copy_licenses()
    run(EXE, "--check-runtime")


def main():
    if sys.platform != "win32" or struct.calcsize("P") != 8 or sys.version_info[:2] != (3, 11):
        raise SystemExit("The Windows PDF runtime requires 64-bit Python 3.11 on the build host.")
    if sys.argv[1:] == ["--freeze"]:
        freeze()
        return
    ROOT.mkdir(parents=True, exist_ok=True)
    fingerprint = hashlib.sha256()
    for file in (Path(__file__), REQUIREMENTS, WORKER):
        fingerprint.update(file.read_bytes())
    fingerprint.update(PYTHON_KEY.encode())
    digest = fingerprint.hexdigest()
    stamp = ROOT / "build.sha256"
    if EXE.is_file() and (EXE.parent / "licenses" / "Python.txt").is_file() and stamp.is_file() and stamp.read_text() == digest:
        return
    stamp.unlink(missing_ok=True)
    venv = ROOT / "venvs" / PYTHON_KEY
    python = venv / "Scripts" / "python.exe"
    if not python.is_file():
        run(sys.executable, "-m", "venv", venv)
    run(python, "-m", "pip", "install", "--disable-pip-version-check", "-r", REQUIREMENTS)
    run(python, __file__, "--freeze")
    stamp.write_text(digest)


if __name__ == "__main__":
    main()
