# pdf-processor Layer

English: [README.md](README.md)

パイプラインが使う poppler のネイティブバイナリを配る Lambda Layer である。
経路 B は Bedrock に渡す前にページをラスタライズし、検証層は抽出結果を原本のテキストレイヤーと突合する。
どちらも poppler に依存する。

## 中身

| パス                     | 用途                                     |
| ------------------------ | ---------------------------------------- |
| `bin/pdftoppm`           | ページを画像にラスタライズする           |
| `bin/pdftotext`          | 埋め込みのテキストレイヤーを抽出する     |
| `bin/pdfinfo`            | ページ数と暗号化の有無を読む             |
| `lib/*.so.*`             | 上記 3 つが依存する共有ライブラリ        |
| `share/poppler/`         | `poppler-data` の CMap と Unicode 変換表 |
| `pdf-processor.manifest` | poppler のバージョンと同梱物の一覧       |

## ビルド

```sh
./build.sh
```

`just` に依存せず単体で実行できる。カレントディレクトリはどこでもよい。
`SKIP_VERIFY=1` を付けると後述の起動確認を省略する。

Docker は Amazon Linux 2023 向けにコンパイルするためだけに使う。
イメージの push はせず ECR リポジトリも作らない。ビルド時の使い捨てである。

再ビルドが必要になるのは poppler のバージョンを上げるときだけである。

## zip の中の階層

Lambda Layer は zip の内容を `/opt` 直下に展開する。
そのため `bin/` `lib/` `share/` を zip のルートに置く必要がある。
単一のディレクトリで包むと `/opt/pdf-processor/bin` となり `PATH` から外れる。

```text
pdf-processor.zip
├── bin/              ->  /opt/bin
├── lib/              ->  /opt/lib
├── share/poppler/    ->  /opt/share/poppler
└── pdf-processor.manifest
```

`provided.al2023` ランタイムは `bin/` と `lib/` を既定で解決するため、環境変数の追加は不要である。
ランタイムイメージに焼かれている既定値は以下となる。

```text
PATH=/var/lang/bin:/usr/local/bin:/usr/bin/:/bin:/opt/bin
LD_LIBRARY_PATH=/var/lang/lib:/lib64:/usr/lib64:/var/runtime:/var/runtime/lib:/var/task:/var/task/lib:/opt/lib
```

`/opt/lib` は末尾にあり、ランタイム側に同名のライブラリがあればそちらが優先される。
したがって glibc とローダはランタイム側のものを使い、意図的に zip から除外している。
2 つ目の glibc を同梱してもバージョンの不一致を生むだけである。
それ以外の `ldd` が返す依存は、Layer を自己完結させるためすべて同梱する。

## poppler のデータディレクトリ

> [!IMPORTANT]
> この件で Lambda 側の環境変数設定は不要である。
> Terraform の compute モジュールで `POPPLER_DATADIR` 等を設定する必要はない。

`poppler-data` は CID キーのフォントを解釈するための CMap と Unicode 変換表を提供する。
これらは `/usr/share/poppler` 配下の単なるデータファイルであり、`ldd` には現れないため共有ライブラリの収集では拾えない。
これが無いと `pdftotext` が日本語の CID フォントを誤読し、経路 B が依存するテキストレイヤーの有無判定が狂う。

poppler 24.08.0 はデータディレクトリを `POPPLER_DATADIR` というビルド時マクロとして `libpoppler` に埋め込む。
環境変数もコマンドラインオプションも持たないことを、配布されているバイナリに対して確認済みである。
Lambda Layer は `/opt` 配下にしか展開できず、それ以外のファイルシステムは読み取り専用であるため、埋め込まれたパスをそのまま満たすことはできない。

そこでビルド時にこの文字列を直接書き換えている。
`/usr/share/poppler` と `/opt/share/poppler` はどちらも 18 バイトであり、ELF のオフセットは一切動かない。
ビルドは次の 3 点を検査し、外れたら失敗する。

- 2 つのパスの長さが等しいこと
- `libpoppler.so.140` の中に旧パスがちょうど 1 件だけ存在すること
- 書き換え後に旧パスが残っておらず、新パスが入っていること

さらに `verify` ステージで構造ではなく挙動を確かめる。
`pdftotext -listenc` に `Shift-JIS` `EUC-JP` `ISO-2022-JP` が現れることを確認しており、
これらは `/opt/share/poppler/unicodeMap` が実際に読まれた場合にしか出てこない。

代替案として `CMAKE_INSTALL_PREFIX` を変えて poppler をソースからビルドする方法があったが採らなかった。
stock rpm に対して AWS が出すセキュリティ更新を手放すことになり、ビルドも大幅に重くなる。
書き換え方式はビルド時の表明と機能検証で同じ結果を得られる。

## サイズ

固定したバージョンでの実測値である。

| 部位                |     サイズ | 備考                          |
| ------------------- | ---------: | ----------------------------- |
| `bin/`              |    0.6 MiB | 実行ファイル 3 つ             |
| `lib/`              |   33.3 MiB | 共有ライブラリ 47 個          |
| `share/poppler/`    |   11.5 MiB | データファイル 259 個         |
| 解凍後の合計        |   45.3 MiB | 上限 250 MB に対して          |
| `pdf-processor.zip` |   16.1 MiB | Lambda がダウンロードする実体 |

意図的に削っていない。
解凍後の合計は、Layer と関数パッケージで共有する 250 MB の枠に対して約 18% であり、現時点でサイズは制約になっていない。

判断の根拠は 2 点ある。

- cairo と X11 系は依存閉包に入らない。
  これらをリンクするのは `pdftocairo` だけで、同梱していない。
  AL2023 の `libharfbuzz.so.0` には `libcairo.so.2` の `DT_NEEDED` が無く、
  rpm メタデータに見える `Requires: cairo` はパッケージ単位の依存であって
  リンク時の依存ではない
- 同梱した 47 個のうち 26 個は実行時に使われない。
  `/opt/lib` の探索順が最後であるため、ローダは `/lib64` 側を解決する。
  `libstdc++.so.6` `libgcc_s.so.1` と、`libpoppler` が署名検証のために
  リンクしている curl / NSS / krb5 / GPGME 一式がこれに当たり、
  合計でおよそ 21 MiB になる

この 26 個を落とせば zip はおよそ半分になる。
しかし `provided.al2023` にどの共有ライブラリが含まれるかを AWS は文書化しておらず、ランタイムイメージはこの Layer とは無関係に更新される。
Layer の再ビルドは poppler のバージョンを上げるときだけであるから、その乖離は時間が経ってから本番障害として現れる。
実測 8 MiB のダウンロードの方が安い側である。
パッケージが 200 MB に近付いたら見直す。

## 検証

`build.sh` は `public.ecr.aws/lambda/provided:al2023` をベースにしたビルドステージを実行する。
組み立てたツリーを `/opt` に配置し、コマンド名だけで各バイナリを起動する。
`PATH` と `LD_LIBRARY_PATH` の既定値を前提として仮定するのではなく、実際に踏ませて確かめている。

ランタイムには `/etc/fonts` が存在せず、fontconfig は組み込みの設定にフォールバックして stderr に出力する場合がある。
検査はこれを許容する。ローダのエラーが出たときだけ失敗し、それ以外は影響を受けない版数の行を要求する。
論文 PDF はフォントを埋め込んでいるため、poppler はシステムフォントの検索に到達する前に短絡する。

zip の生成後は `unzip -l` の結果と各バイナリの ELF アーキテクチャを表示する。
`ARM aarch64` と出れば正しい。

手元で既存の zip を調べる場合は以下となる。

```sh
unzip -l pdf-processor.zip
unzip -p pdf-processor.zip pdf-processor.manifest
unzip -p pdf-processor.zip bin/pdftoppm | file -b -
```

## バージョンの固定

Amazon Linux 2023 のベースイメージのタグ、`poppler-utils` と `poppler-data` の rpm バージョンを、
`Dockerfile` の `ARG` の既定値として固定している。
ベースイメージのタグを日付まで固定すると dnf のリポジトリのスナップショットも固定されるため、
同じ `Dockerfile` から同じパッケージ構成が再現される。
zip 化の前にファイルの mtime を揃えており、`--no-cache` での再ビルドが同一バイトの zip を生むことを確認済みである。
Terraform の `source_code_hash` は中身が実際に変わったときだけ動く。

## 制約

- CJK フォントは含まない。
  日本語 PDF のラスタライズには別途 `fonts-noto-cjk` Layer が要るが、
  Phase 1 の対象外である。
  ここに入れた CMap が担うのはテキスト抽出であり、グリフの描画ではない
- zip の S3 へのアップロードと Layer の発行はこのディレクトリの責務ではない
