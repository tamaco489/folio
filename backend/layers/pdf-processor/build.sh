#!/usr/bin/env bash
#
# pdf-processor Layer の zip をビルドする
#
# 使い方
#   ./build.sh                  検証込みでビルドする
#   SKIP_VERIFY=1 ./build.sh    provided.al2023 での起動確認を省く
#
# just などのタスクランナーに依存せず単体で実行できる
# カレントディレクトリはどこでもよい (スクリプトの位置を基準に動く)
#
# 出力はこのスクリプトと同じ階層の pdf-processor.zip で、中身は以下の 4 つである
#
#   bin/                    Layer 展開後は /opt/bin (PATH に含まれる)
#   lib/                    Layer 展開後は /opt/lib (LD_LIBRARY_PATH に含まれる)
#   share/poppler/          Layer 展開後は /opt/share/poppler (CMap と Unicode 変換表)
#   pdf-processor.manifest  poppler のバージョンと同梱物の一覧
#
# zip の中身は bin/ lib/ share/ という相対パスで、Layer は zip の内容を /opt 直下に展開する
# したがって /opt/bin/pdftoppm と /opt/lib/libpoppler.so.140 という配置になる
# ルートに単一のディレクトリを挟むと /opt/pdf-processor/bin となり PATH から外れる
#
# zip の S3 へのアップロードと Lambda Layer の発行はこのスクリプトの責務ではない
# インフラ側 (Terraform) から参照する

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir"

layer_name="pdf-processor"
zip_name="${layer_name}.zip"
platform="linux/arm64"

if ! command -v docker > /dev/null 2>&1; then
    printf 'docker が見つからない\n' >&2
    exit 1
fi

if ! docker buildx version > /dev/null 2>&1; then
    printf 'docker buildx が見つからない (--output type=local に必要)\n' >&2
    exit 1
fi

if [ "${SKIP_VERIFY:-0}" != "1" ]; then
    printf '==> provided.al2023 上で起動を確認する\n'
    # イメージを残す必要はないため成果物は捨てる
    docker buildx build \
        --platform "$platform" \
        --target verify \
        --output type=cacheonly \
        .
fi

printf '==> zip を生成する\n'
rm -f "$zip_name"
docker buildx build \
    --platform "$platform" \
    --target export \
    --output "type=local,dest=${script_dir}" \
    .

if [ ! -f "$zip_name" ]; then
    printf '%s が生成されなかった\n' "$zip_name" >&2
    exit 1
fi

printf '==> %s の中身\n' "$zip_name"
unzip -l "$zip_name"

if command -v file > /dev/null 2>&1; then
    printf '==> 実行ファイルのアーキテクチャ\n'
    for b in pdftoppm pdftotext pdfinfo; do
        printf '  bin/%s: %s\n' "$b" "$(unzip -p "$zip_name" "bin/${b}" | file -b -)"
    done
fi

printf '==> 完了: %s/%s\n' "$script_dir" "$zip_name"
