locals {
  name_prefix = "${var.env}-folio"

  # バケット名は全アカウントで一意である必要があるため、末尾にアカウント ID を付ける
  documents_bucket_name = "${local.name_prefix}-documents-${var.account_id}"
  artifacts_bucket_name = "${local.name_prefix}-artifacts-${var.account_id}"
  jobs_table_name       = "${local.name_prefix}-jobs"

  # S3 キーの第 1 階層 (backend/internal/awsx/s3/keys.go と対応)
  #   uploads/{jobId}/original.pdf  受領した PDF (イベント発火点)
  #   work/{jobId}/...              ページ画像・Textract 生出力・中間 JSON (finalizer 完了後は参照されない)
  #   outputs/{jobId}/...           両経路の抽出結果と比較結果
  # バケットを分けずプレフィックスで役割を分けるのは、通知を uploads/ に限定できれば十分なため
  # ライフサイクルで参照するのは work/ だけなので、他 2 つは local にしない
  prefix_work = "work/"

  # work/ の失効日数
  # work/ は 1 論文で数十 MB のページ画像を含み、finalizer 完了後は読まれない
  # FAILED からの再投入は work/ の中間結果を finalizer が拾うため、失効後の再投入は Parallel からの作り直しになるが、動作としては正しい
  # 評価用の再投入は数日から数週間の範囲に収まる想定なので 30 日とし、変数にはしない
  work_expiration_days = 30

  # 中断したマルチパートアップロードの残骸を消すまでの日数
  # 残骸は一覧に出ないまま課金され続けるため、プレフィックスによらずバケット全体に掛ける
  abort_incomplete_multipart_days = 7

  # artifacts の非現行バージョン (上書きされた旧 zip) を消すまでの日数
  # Lambda は関数の更新と Layer の発行の時点で zip を取り込むため、その後に S3 の旧版が消えても動作に影響しない
  # 旧版を残す用途は s3_object_version を巻き戻す手動ロールバックだけで、検証環境では 1 週間より前の版に戻す想定はない
  # 消さないと CI がアップロードするたびに数十 MB の Layer zip が積み上がる
  artifacts_noncurrent_expiration_days = 7

  # dev は評価用の使い捨て環境として扱い、それ以外は誤削除から保護する
  # dev でも消えて困るのは outputs/ だが、jobId が SHA-256 で決まるため同じ PDF の再投入で同じ結果を作り直せる
  is_disposable = var.env == "dev"
}
