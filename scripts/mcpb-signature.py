#!/usr/bin/env python3
"""Sign an MCPB as a valid ZIP and verify its detached CMS signature.

MCPB 2.1.2 appends its signature after the ZIP end record without updating the
comment length, and its verifier calls an unimplemented node-forge method. The
signature block is made the ZIP comment before signing so strict ZIP readers
can open it; OpenSSL verifies the actual detached signature.
"""

import argparse
import io
import os
import struct
import subprocess
import sys
import tempfile
import zipfile
import zlib
from pathlib import Path

HEADER = b"MCPB_SIG_V1"
FOOTER = b"MCPB_SIG_END"
EOCD = b"PK\x05\x06"


def run(*args: str, capture: bool = False) -> bytes:
    result = subprocess.run(
        args, check=True, stdout=subprocess.PIPE if capture else None
    )
    return result.stdout if capture else b""


def split_signature(data: bytes) -> tuple[bytes, bytes, bytes]:
    if not data.endswith(FOOTER):
        raise ValueError("MCPB signature footer is missing")
    start = data.rfind(HEADER)
    if start < 0:
        raise ValueError("MCPB signature header is missing")
    length_pos = start + len(HEADER)
    if length_pos + 4 > len(data):
        raise ValueError("MCPB signature length is missing")
    length = struct.unpack_from("<I", data, length_pos)[0]
    if length_pos + 4 + length + len(FOOTER) != len(data):
        raise ValueError("MCPB signature length does not match the file")
    signature_start = length_pos + 4
    signature_end = len(data) - len(FOOTER)
    return data[:start], data[signature_start:signature_end], data[start:]


def check_zip(data: bytes, comment: bytes) -> None:
    original = data[: -len(comment)] if comment else data
    if len(original) < 22 or original[-22:-18] != EOCD:
        raise ValueError("expected a ZIP with a final end record")
    declared = struct.unpack_from("<H", original, len(original) - 2)[0]
    if declared != len(comment):
        raise ValueError(
            f"ZIP comment length {declared} differs from signature length "
            f"{len(comment)}"
        )
    try:
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            if archive.comment != comment or archive.testzip() is not None:
                raise ValueError("MCPB ZIP comment or contents are invalid")
    except (EOFError, zlib.error) as error:
        raise ValueError(f"invalid MCPB ZIP contents: {error}") from error


def sign(bundle: Path, cert: Path, key: Path) -> None:
    unsigned = bundle.read_bytes()
    check_zip(unsigned, b"")

    # The DER length is stable for a given key and certificate. A first pass
    # obtains the block length; the final pass signs the already patched EOCD.
    with tempfile.TemporaryDirectory(prefix="mcpb-sign-") as directory:
        probe = Path(directory) / "probe.mcpb"
        probe.write_bytes(unsigned)
        run("mcpb", "sign", str(probe), "--cert", str(cert), "--key", str(key))
        _, _, block = split_signature(probe.read_bytes())

    if len(block) > 65535:
        raise ValueError("MCPB signature exceeds the ZIP comment limit")
    patched = bytearray(unsigned)
    struct.pack_into("<H", patched, len(patched) - 2, len(block))
    bundle.write_bytes(patched)
    run("mcpb", "sign", str(bundle), "--cert", str(cert), "--key", str(key))
    content, _, final_block = split_signature(bundle.read_bytes())
    if len(final_block) != len(block) or content != patched:
        raise ValueError("MCPB signing changed size or content between passes")
    check_zip(bundle.read_bytes(), final_block)
    print(f"Signed ZIP-valid MCPB: {bundle}")


def verify(bundle: Path, cert: Path) -> None:
    data = bundle.read_bytes()
    content, signature, block = split_signature(data)
    check_zip(data, block)

    with tempfile.TemporaryDirectory(prefix="mcpb-verify-") as directory:
        directory_path = Path(directory)
        content_path = directory_path / "content.zip"
        signature_path = directory_path / "signature.der"
        signer_path = directory_path / "signer.pem"
        content_path.write_bytes(content)
        signature_path.write_bytes(signature)
        run(
            "openssl",
            "cms",
            "-verify",
            "-binary",
            "-inform",
            "DER",
            "-in",
            str(signature_path),
            "-content",
            str(content_path),
            "-signer",
            str(signer_path),
            "-noverify",
            "-out",
            os.devnull,
        )
        signer_der = run(
            "openssl",
            "x509",
            "-in",
            str(signer_path),
            "-outform",
            "DER",
            capture=True,
        )
        expected_der = run(
            "openssl",
            "x509",
            "-in",
            str(cert),
            "-outform",
            "DER",
            capture=True,
        )
        if signer_der != expected_der:
            raise ValueError("MCPB signer differs from release certificate")
    print(f"Verified MCPB ZIP, CMS signature and signer: {bundle}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    actions = parser.add_subparsers(dest="action", required=True)
    sign_parser = actions.add_parser("sign")
    sign_parser.add_argument("bundle", type=Path)
    sign_parser.add_argument("--cert", type=Path, required=True)
    sign_parser.add_argument("--key", type=Path, required=True)
    verify_parser = actions.add_parser("verify")
    verify_parser.add_argument("bundle", type=Path)
    verify_parser.add_argument("--cert", type=Path, required=True)
    args = parser.parse_args()
    try:
        if args.action == "sign":
            sign(args.bundle, args.cert, args.key)
        else:
            verify(args.bundle, args.cert)
    except (
        OSError,
        ValueError,
        subprocess.CalledProcessError,
        zipfile.BadZipFile,
    ) as error:
        print(f"MCPB {args.action} failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
