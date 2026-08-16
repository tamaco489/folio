# アカウント ID はここに書かない (公開リポジトリのため)
# 環境変数 TF_VAR_account_id で渡す。region は variables.tf の default (us-east-1) を使う
# crossref_mailto もメールアドレスなので書かず、必要なら TF_VAR_crossref_mailto で渡す
env = "dev"

# backend の記録済みレスポンス (backend/testdata/bedrock) と同じクロスリージョン推論プロファイル
bedrock_model_id = "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
