# アカウント ID はここに書かない (公開リポジトリのため)
# 環境変数 TF_VAR_account_id で渡す。region は variables.tf の default (us-east-1) を使う
# crossref_mailto もメールアドレスなので書かず、必要なら TF_VAR_crossref_mailto で渡す
env = "dev"

# backend の記録済みレスポンス (backend/testdata/bedrock) と同じクロスリージョン推論プロファイル
bedrock_model_id = "us.anthropic.claude-sonnet-4-5-20250929-v1:0"

# GitHub Actions 用ロールの信頼ポリシーで sub を絞るリポジトリ (公開値)
# このリポジトリは 2026-07-15 以降の作成で sub が immutable subject claim (owner@id/repo@id) の形になるため、名前だけでなく ID 付きで書く
# 値は gh api repos/tamaco489/folio/actions/oidc/customization/sub --jq .sub_claim_prefix の repo: を除いた部分
github_repository = "tamaco489@189016912/folio@1334480011"
