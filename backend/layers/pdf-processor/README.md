# pdf-processor Layer

日本語版: [README.ja.md](README.ja.md)

Lambda Layer that ships the poppler native binaries used by the pipeline.
Route B rasterizes pages before handing them to Bedrock, and the
verification stage compares the extracted result against the original text
layer. Both depend on poppler.

## Contents

| Path                     | Purpose                                       |
| ------------------------ | --------------------------------------------- |
| `bin/pdftoppm`           | Rasterizes pages to images                    |
| `bin/pdftotext`          | Extracts the embedded text layer              |
| `bin/pdfinfo`            | Reads page count and encryption state         |
| `lib/*.so.*`             | Shared libraries the three binaries depend on |
| `share/poppler/`         | CMaps and Unicode tables from `poppler-data`  |
| `pdf-processor.manifest` | poppler version and the list of bundled files |

## Build

```sh
./build.sh
```

The script is standalone and does not require `just`. It works from any
working directory. Set `SKIP_VERIFY=1` to skip the runtime check described
below.

Docker is only used to compile against Amazon Linux 2023. No image is pushed
and no ECR repository exists; the images are throwaway build inputs.

Rebuilding is only needed when the poppler version is bumped.

## Layout inside the zip

A Lambda Layer extracts the zip directly under `/opt`, so the archive must
have `bin/`, `lib/` and `share/` at its root. Wrapping them in a single
top-level directory would produce `/opt/pdf-processor/bin`, which is not on
`PATH`.

```text
pdf-processor.zip
├── bin/              ->  /opt/bin
├── lib/              ->  /opt/lib
├── share/poppler/    ->  /opt/share/poppler
└── pdf-processor.manifest
```

The `provided.al2023` runtime already resolves `bin/` and `lib/`, so no extra
environment variable is needed. The defaults baked into the runtime image
are:

```text
PATH=/var/lang/bin:/usr/local/bin:/usr/bin/:/bin:/opt/bin
LD_LIBRARY_PATH=/var/lang/lib:/lib64:/usr/lib64:/var/runtime:/var/runtime/lib:/var/task:/var/task/lib:/opt/lib
```

`/opt/lib` comes last, so system libraries win where the runtime provides
them. glibc and the dynamic loader are therefore taken from the runtime and
deliberately excluded from the archive; bundling a second glibc would only
create a version mismatch. Everything else reported by `ldd` is copied in so
that the Layer stays self-contained.

## poppler data directory

> [!IMPORTANT]
> No Lambda environment variable is required for this. The Terraform compute
> module does not need to set `POPPLER_DATADIR` or anything equivalent.

`poppler-data` provides the CID CMaps and Unicode conversion tables that
poppler needs to decode CID-keyed fonts. They are plain data files under
`/usr/share/poppler`, so they never appear in `ldd` output and are not picked
up by the shared library sweep. Without them `pdftotext` misreads Japanese
CID fonts, which would corrupt the text-layer presence check that route B
relies on.

poppler 24.08.0 bakes the data directory into `libpoppler` as the
`POPPLER_DATADIR` compile-time macro. It reads no environment variable and
exposes no command-line option for it; both were checked against the shipped
binary. Since a Lambda Layer can only extract under `/opt` and the rest of
the filesystem is read only, the compiled-in path cannot be satisfied as is.

The build therefore rewrites that string in place. `/usr/share/poppler` and
`/opt/share/poppler` are both 18 bytes, so the replacement does not move any
ELF offset. The build asserts that the string occurs exactly once in
`libpoppler.so.140`, that the two paths are the same length, and that the old
path is gone afterwards. The `verify` stage then confirms the rewrite
functionally rather than structurally, by checking that `pdftotext -listenc`
reports `Shift-JIS`, `EUC-JP` and `ISO-2022-JP` — encodings that only exist
if `/opt/share/poppler/unicodeMap` was actually read.

The alternative was to build poppler from source with a different
`CMAKE_INSTALL_PREFIX`. That was rejected because it forfeits the security
updates AWS ships for the stock rpm and makes the build far heavier, for a
result the in-place rewrite achieves with a build-time assertion and a
functional check.

## Size

Measured on the pinned versions:

| Part                     |     Size | Notes                            |
| ------------------------ | -------: | -------------------------------- |
| `bin/`                   |  0.6 MiB | 3 executables                    |
| `lib/`                   | 33.3 MiB | 47 shared libraries              |
| `share/poppler/`         | 11.5 MiB | 259 data files, mostly CMaps     |
| Total, unzipped          | 45.3 MiB | Against a 250 MB limit           |
| `pdf-processor.zip`      | 16.1 MiB | What Lambda downloads            |

Nothing is trimmed, deliberately. The unzipped total is roughly 18% of the
250 MB budget that the Layer shares with the function package, so size is not
a constraint today.

Two observations behind that call:

- cairo and the X11 stack do not enter the closure. Only `pdftocairo` links
  them, and it is not shipped. AL2023's `libharfbuzz.so.0` has no
  `DT_NEEDED` entry for `libcairo.so.2` — the `Requires: cairo` seen in the
  rpm metadata is a package-level dependency, not a link-time one.
- 26 of the 47 bundled libraries are shadowed at runtime: because `/opt/lib`
  is searched last, the loader resolves them from `/lib64` instead. That
  includes `libstdc++.so.6`, `libgcc_s.so.1` and the whole
  curl/NSS/krb5/GPGME cluster that `libpoppler` links for signature
  verification, and accounts for roughly 21 MiB.

Dropping those 26 would cut the archive by about half, but AWS does not
document which shared libraries `provided.al2023` contains, and the runtime
image is updated independently of this Layer. Since the Layer is only rebuilt
on a poppler bump, such drift would surface as a production failure long
after the fact. A measured 8 MiB of download is the cheaper side of that
trade. Revisit if the package approaches 200 MB.

## Verification

`build.sh` runs a build stage based on
`public.ecr.aws/lambda/provided:al2023` that copies the staged tree to `/opt`
and invokes each binary by bare command name. That exercises the real `PATH`
and `LD_LIBRARY_PATH` defaults rather than an assumption about them.

`/etc/fonts` does not exist in the runtime, so fontconfig falls back to its
built-in configuration and may write to stderr. The check tolerates that: it
fails only on a loader error, and otherwise requires the version banner on
stdout, which is unaffected. Academic PDFs embed their fonts, so poppler
short-circuits before reaching a system font lookup.

After the archive is produced the script prints `unzip -l` and the ELF
architecture of each binary, which should read `ARM aarch64`.

To inspect an existing archive by hand:

```sh
unzip -l pdf-processor.zip
unzip -p pdf-processor.zip pdf-processor.manifest
unzip -p pdf-processor.zip bin/pdftoppm | file -b -
```

## Version pinning

The Amazon Linux 2023 base image tag, the `poppler-utils` rpm version and the
`poppler-data` rpm version are pinned in the `Dockerfile` as `ARG` defaults.
Pinning the base image tag down to the date also freezes the dnf repository
snapshot, so the same `Dockerfile` reproduces the same package set. File
timestamps are normalized before zipping; a `--no-cache` rebuild was
confirmed to produce a byte-identical archive, so Terraform's
`source_code_hash` only changes when the contents really do.

## Limitations

- CJK fonts are not included. Rasterizing Japanese PDFs needs a separate
  `fonts-noto-cjk` Layer, which is out of scope for Phase 1. The CMaps
  shipped here cover text extraction, not glyph rendering.
- Uploading the archive to S3 and publishing the Layer are done outside this
  directory.
