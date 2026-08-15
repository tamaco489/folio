# syntax=docker/dockerfile:1

# pdf-processor Layer のビルド定義
#
# poppler の pdftoppm / pdftotext / pdfinfo と、その依存共有ライブラリを取り出して zip に固める
# イメージは保存も配布もせず、ビルドのためだけに使う使い捨てである
#
# ステージ
#   build   AL2023 (arm64) 上で poppler-utils を導入し /layer に bin/ lib/ を組んで zip 化する
#   verify  実際の provided.al2023 ランタイムイメージの /opt に配置し、起動できることを確かめる
#   export  zip だけを持つ scratch で、build.sh が --output type=local で取り出す
#
# /layer 以下の階層はそのまま Layer の展開結果になる
#   /layer/bin    -> /opt/bin    (provided.al2023 の PATH に含まれる)
#   /layer/lib    -> /opt/lib    (provided.al2023 の LD_LIBRARY_PATH の末尾に含まれる)
#   /layer/share  -> /opt/share  (poppler の CMap 等のデータファイル)

# Lambda ランタイム provided.al2023 と同系列の Amazon Linux 2023
# タグを日付まで固定すると dnf が参照するリポジトリのスナップショットも固定され、
# 同じ Dockerfile から同じパッケージ構成が再現される
ARG AL2023_TAG=2023.12.20260803.3

# AL2023 core リポジトリの poppler-utils の rpm バージョン (upstream poppler は 24.08.0)
ARG POPPLER_VERSION=24.08.0-1.amzn2023.0.1

# CID フォントの CMap と Unicode 変換表で、日本語 PDF のテキストレイヤー判定に要る
ARG POPPLER_DATA_VERSION=0.4.9-7.amzn2023.0.2


FROM public.ecr.aws/amazonlinux/amazonlinux:${AL2023_TAG} AS build

ARG POPPLER_VERSION
ARG POPPLER_DATA_VERSION

# ホストが x86_64 でも --platform 指定を忘れると気付かず amd64 の Layer ができるため落とす
RUN test "$(uname -m)" = "aarch64"

RUN dnf -y install \
        "poppler-utils-${POPPLER_VERSION}" \
        "poppler-data-${POPPLER_DATA_VERSION}" \
        zip \
    && dnf clean all

RUN <<'SH'
#!/usr/bin/env bash
set -euo pipefail

mkdir -p /layer/bin /layer/lib

# 3 つとも poppler-utils が /usr/bin に提供する
# pdfinfo は internal/pipeline/pdf がページ数と暗号化の判定に使うため必須である
for b in pdftoppm pdftotext pdfinfo; do
    cp -L "/usr/bin/${b}" "/layer/bin/${b}"
done

# ldd は推移的な依存を平坦化して列挙するため、実行ファイル 3 つ分の出力で閉包が得られる
# glibc とローダは Lambda ランタイム側のものを使うため同梱しない
# 2 つ目の glibc を持ち込んでもバージョンの不一致を生むだけである
ldd /layer/bin/* \
    | awk '/=> \//{ print $3 }' \
    | sort -u \
    | while read -r lib; do
        case "$(basename "$lib")" in
            ld-linux-*|libc.so.*|libm.so.*|libdl.so.*|libpthread.so.*|librt.so.*|libresolv.so.*|libutil.so.*)
                continue
                ;;
        esac
        # ldd が返すパスの basename は SONAME と一致するため、-L で実体を辿っても参照名は保たれる
        cp -L "$lib" "/layer/lib/$(basename "$lib")"
    done

if ldd /layer/bin/* | grep -q 'not found'; then
    printf '解決できない依存が残っている\n' >&2
    exit 1
fi

chmod 0755 /layer/bin/* /layer/lib/*
SH

# poppler のデータディレクトリを /opt へ移す
#
# CMap は ldd に現れないデータファイルなので共有ライブラリの収集では拾えない
# poppler はこのディレクトリを POPPLER_DATADIR マクロとしてビルド時に埋め込んでおり、
# 24.08.0 には環境変数もコマンドラインオプションも存在しない (-h と .so の文字列で確認済み)
# Lambda は /opt 以外に Layer を展開できず、/usr/share は読み取り専用のため、
# 埋め込み文字列そのものを書き換えて参照先を移す
#
# /usr/share/poppler と /opt/share/poppler はどちらも 18 バイトで長さが同じであり、
# ELF のオフセットを動かさずに上書きできる
# 該当の文字列は libpoppler.so.140 に 1 件だけ存在する
RUN <<'SH'
#!/usr/bin/env bash
set -euo pipefail

old_dir=/usr/share/poppler
new_dir=/opt/share/poppler

test "${#old_dir}" -eq "${#new_dir}"

mkdir -p /layer/share
cp -a "$old_dir" /layer/share/poppler

lib="$(ls /layer/lib/libpoppler.so.* | head -1)"

offsets="$(grep -abo "$old_dir" "$lib" | cut -d: -f1 || true)"
count="$(printf '%s' "$offsets" | grep -c . || true)"
if [ "$count" != "1" ]; then
    printf '%s の中の %s が %s 件見つかった (1 件を期待)\n' "$lib" "$old_dir" "$count" >&2
    exit 1
fi

printf '%s' "$new_dir" | dd of="$lib" bs=1 seek="$offsets" conv=notrunc status=none

if grep -aq "$old_dir" "$lib"; then
    printf '%s の書き換えに失敗した\n' "$lib" >&2
    exit 1
fi
if ! grep -aq "$new_dir" "$lib"; then
    printf '%s に %s が入っていない\n' "$lib" "$new_dir" >&2
    exit 1
fi
SH

RUN <<'SH'
#!/usr/bin/env bash
set -euo pipefail

{
    printf 'layer: pdf-processor\n'
    printf 'poppler-utils: %s\n' "$(rpm -q --qf '%{VERSION}-%{RELEASE}.%{ARCH}' poppler-utils)"
    printf 'poppler-data: %s\n' "$(rpm -q --qf '%{VERSION}-%{RELEASE}.%{ARCH}' poppler-data)"
    printf 'base: %s\n' "$(cat /etc/system-release)"
    printf 'datadir: /opt/share/poppler\n'
    printf 'binaries:\n'
    for f in /layer/bin/*; do printf '  bin/%s\n' "$(basename "$f")"; done
    printf 'libraries:\n'
    for f in /layer/lib/*; do printf '  lib/%s\n' "$(basename "$f")"; done
} > /layer/pdf-processor.manifest
SH

# mtime を固定して zip のハッシュを安定させ、中身の変わらない再ビルドが
# Terraform の source_code_hash で差分と判定されるのを避ける (zip の下限は 1980-01-01)
RUN <<'SH'
#!/usr/bin/env bash
set -euo pipefail

# amazonlinux:2023 のベースには findutils が入っていないため bash の globstar で代替する
shopt -s globstar dotglob nullglob
touch -t 198001010000 /layer /layer/**

cd /layer
zip -r9 -X /tmp/layer.zip bin lib share pdf-processor.manifest
SH


FROM public.ecr.aws/lambda/provided:al2023 AS verify

COPY --from=build /layer/bin /opt/bin
COPY --from=build /layer/lib /opt/lib
COPY --from=build /layer/share /opt/share

# provided.al2023 の既定値は PATH に /opt/bin、LD_LIBRARY_PATH の末尾に /opt/lib を含む
# 環境変数を足さずにコマンド名だけで起動できるかを見ることで、その前提ごと検証する
RUN <<'SH'
set -eu

for b in pdftoppm pdftotext pdfinfo; do
    # poppler の -v は終了コードが版によって異なるため出力で成否を判定する
    # fontconfig は /etc/fonts が無い環境で stderr に警告を出すが、
    # 版数の行が出ていれば動的リンクは解決できている
    out="$("$b" -v 2>&1 || true)"
    printf '%s\n' "$out"
    case "$out" in
        *"error while loading shared libraries"*|*"cannot open shared object"*)
            printf '%s の共有ライブラリを解決できない\n' "$b" >&2
            exit 1
            ;;
    esac
    case "$out" in
        *"$b version"*) ;;
        *)
            printf '%s の起動に失敗した\n' "$b" >&2
            exit 1
            ;;
    esac
done

# 埋め込みパスの書き換えが効いていれば /opt/share/poppler/unicodeMap が読まれ、
# 組み込み以外のエンコーディングが一覧に現れる
# CMap が解決できるかどうかをこれで代表させる
encs="$(pdftotext -listenc 2>&1 || true)"
printf '%s\n' "$encs"
for e in Shift-JIS EUC-JP ISO-2022-JP; do
    case "$encs" in
        *"$e"*) ;;
        *)
            printf '%s が無く /opt/share/poppler が参照されていない\n' "$e" >&2
            exit 1
            ;;
    esac
done
SH


FROM scratch AS export

COPY --from=build /tmp/layer.zip /pdf-processor.zip
