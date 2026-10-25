"""Exact supported executable identities and byte patches for MS2 and Finale."""
from __future__ import annotations

from .common import BytePatch, ExeProfile

MS2_PROFILE = ExeProfile(
    key="ms2",
    display_name="Mary Skelter 2",
    input_filename="MarySkelter2.exe",
    default_output_filename="MarySkelter2.qol.exe",
    original_sha256="07b97dd3b4aa9e50249a2fcfb619b31822eb7219ebeae150b9b9c421a5b5834c",
    destub_sha256="c24df22f43524e236925bf38ab8cf9d6171ba3518e6f1b5828bdf740d95cffbc",
    final_sha256="81b6641fadff2767bb23492b1c6b0a9cac645a7496c65ed9d8eef2144ca94fa8",
    patches=(
        BytePatch(
            rva=0x25B2BB,
            expected=bytes.fromhex("8b4140"),
            replacement=bytes.fromhex("6a1b58"),
            label="skip startup logos",
            message="patching startup LogoStage -> final state 27",
        ),
        BytePatch(
            rva=0x2AA27A,
            expected=bytes.fromhex("488d0daf1b7100"),
            replacement=bytes.fromhex("488d0def1a7100"),
            label="skip boot VA prompt",
            message="bypassing boot VA selector -> normal continuation (voice state 0 = Japanese)",
        ),
    ),
    late_patches=(
        BytePatch(
            rva=0x000BFE0E,
            expected=bytes.fromhex("488d155b696400488d0d0c5b2a01e8bf7c2800"),
            replacement=b"\x90" * 19,
            label="skip Voice_EN.bra mount",
            message="skipping unused English voice archive mount",
        ),
    ),
)

FINALE_PROFILE = ExeProfile(
    key="finale",
    display_name="Mary Skelter Finale",
    input_filename="MarySkelterFinale.exe",
    default_output_filename="MarySkelterFinale.qol.exe",
    original_sha256="5c16abfdc3f355404c2c7faa6b277e5023812d2292cb8c5d4ad5177ba6c05a54",
    destub_sha256="900a2cf0485fe882285e2409b2105eda52d44063c378d4ea9e9e1db052f2fa25",
    final_sha256="cc0faf4df3c94e64a0084092e6afdb8c34dee9abe9f00a9f9c429cc5957c570c",
    patches=(
        BytePatch(
            rva=0x29B248,
            expected=bytes.fromhex("01"),
            replacement=bytes.fromhex("1a"),
            label="skip startup logos",
            message="skipping startup creator logos (LogoStage -> completion state 26)",
        ),
        BytePatch(
            rva=0x11FC81,
            expected=bytes.fromhex("0c"),
            replacement=bytes.fromhex("ff"),
            label="Japanese voice resource variant",
            message="keeping Japanese voice resource variant",
        ),
    ),
    late_patches=(
        BytePatch(
            rva=0x00091F10,
            expected=bytes.fromhex("488d15d9eb8a00488d0dc2d00f01e84d162f00"),
            replacement=b"\x90" * 19,
            label="skip Sound_EN.bra mount",
            message="skipping unused English sound archive mount",
        ),
    ),
)

PROFILES = {profile.key: profile for profile in (MS2_PROFILE, FINALE_PROFILE)}
