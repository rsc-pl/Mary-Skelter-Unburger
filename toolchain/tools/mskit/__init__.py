"""mskit — Mary Skelter text toolchain core library.

Pure Python 3 standard library (zlib only). Layers, innermost first:

    GDAT  ->  BILZ  ->  CL3  ->  { STCM2, GBNL/GBIN, GSTR, ENC, ... }

Every parser here is *lossless by construction*: parsed structures keep the
original byte ranges so that unchanged resources can be re-emitted verbatim.
"""

__all__ = ["bilz", "gdat", "cl3", "stcm", "gbnl", "enc", "corpus", "records"]
