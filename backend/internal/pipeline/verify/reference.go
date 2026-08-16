package verify

import (
	"context"
	"errors"
	"fmt"

	"github.com/tamaco489/folio/backend/internal/domain"
	"github.com/tamaco489/folio/backend/internal/pipeline/verify/crossref"
)

const (
	// MaxLookups は 1 文書あたり Crossref に問い合わせる参照文献の上限
	//
	// 直列 + 最小間隔 350ms + 応答待ちで 1 件 1 秒弱、リトライを含めても finalizer の Lambda (15 分) に収まる件数として置く
	// 超えた分は照合を試みず skipped にする
	MaxLookups = 100

	// maxConsecutiveUnavailable は Crossref が続けて応答しなかった場合に残りの照合を諦める回数
	//
	// 障害中に 1 件ずつタイムアウトとリトライを繰り返すと上限件数 x 30 秒を超えうるため、早めに見切って unavailable のまま続行する
	maxConsecutiveUnavailable = 3

	// titleMatchThreshold は Crossref の登録題名が抽出した記載に含まれているとみなす被覆率
	//
	// 登録題名のトークン対のうち 7 割が抽出した題名 (無ければ生の記載) に見つかれば同じ文献とみなす
	// 4 語の題名で 1 語の抽出ミス (対が 1 つ崩れて 2/3) は許容せず、5 語以上なら 1 語のミス (3/4) を許容する境で置いた
	// 副題の有無や句読点の差は対の大半が保たれるため超え、別の文献は語彙が重なっても対が崩れるため超えない
	titleMatchThreshold = 0.7
)

// Resolver は Crossref への問い合わせ口
//
// crossref.Client がこれを満たす。テストではフェイクに差し替える
type Resolver interface {
	LookupDOI(ctx context.Context, doi string) (crossref.Work, error)
	Search(ctx context.Context, bibliographic string) ([]crossref.Work, error)
}

// checkReferences は参照文献を 1 件ずつ Crossref と照合する
//
// エラーは返さず、照合できなかった件は状態として残して続行する
// ctx がキャンセルされたら残りを unavailable にして早く抜ける
func (v *Verifier) checkReferences(ctx context.Context, refs []domain.Reference) []ReferenceCheck {
	checks := make([]ReferenceCheck, 0, len(refs))
	failures := 0
	for i, ref := range refs {
		c := ReferenceCheck{Index: i}
		if ref.DOI != nil {
			c.DOI = *ref.DOI
		}
		switch {
		case v.resolver == nil:
			c.Status, c.Detail = StatusSkipped, "Resolver が設定されていない"
		case i >= MaxLookups:
			c.Status, c.Detail = StatusSkipped, fmt.Sprintf("上限 %d 件を超えた", MaxLookups)
		case ctx.Err() != nil:
			c.Status, c.Detail = StatusUnavailable, ctx.Err().Error()
		case failures >= maxConsecutiveUnavailable:
			c.Status, c.Detail = StatusUnavailable, fmt.Sprintf("Crossref が %d 件続けて応答しなかったため省略した", failures)
		default:
			v.checkReference(ctx, ref, &c)
		}
		if c.Status == StatusUnavailable {
			failures++
		} else if c.Status != StatusSkipped {
			failures = 0
		}
		checks = append(checks, c)
	}
	return checks
}

// checkReference は 1 件を照合し、結果を c に書く
//
// DOI があればその登録題名と抽出した記載を比べる。DOI が実在しても題名が別物なら mismatch とし、DOI が別の文献を指していると記録する
// DOI が無ければ題名 (無ければ生の記載) で検索し、上位候補のいずれかの題名が一致すれば verified、無ければ notFound とする
func (v *Verifier) checkReference(ctx context.Context, ref domain.Reference, c *ReferenceCheck) {
	// 登録題名を照らす先は抽出した題名と生の記載の両方 (題名が取れていなくても Raw に題名が含まれる)
	extracted := newCorpus(ref.Title + "\n" + ref.Raw)

	if c.DOI != "" {
		work, err := v.resolver.LookupDOI(ctx, c.DOI)
		if err != nil {
			c.Status, c.Detail = statusOf(err), err.Error()
			return
		}
		c.MatchedDOI, c.MatchedTitle = work.DOI, work.Title
		c.Status = StatusVerified
		score, ok := extracted.coverage(work.Title)
		if !ok || len(extracted.tokens) == 0 {
			c.Detail = "登録題名か抽出した記載が空で比べられないため DOI の実在だけを確かめた"
			return
		}
		c.Similarity = &score
		if score < titleMatchThreshold {
			c.Status = StatusMismatch
		}
		return
	}

	c.Query = ref.Title
	if c.Query == "" {
		c.Query = ref.Raw
	}
	if c.Query == "" {
		c.Status, c.Detail = StatusSkipped, "DOI も題名も生の記載も無い"
		return
	}
	works, err := v.resolver.Search(ctx, c.Query)
	if err != nil {
		c.Status, c.Detail = statusOf(err), err.Error()
		return
	}
	c.Status = StatusNotFound
	for _, w := range works {
		score, ok := extracted.coverage(w.Title)
		if !ok {
			continue
		}
		if c.Similarity == nil || score > *c.Similarity {
			c.Similarity, c.MatchedDOI, c.MatchedTitle = &score, w.DOI, w.Title
		}
		if score >= titleMatchThreshold {
			c.Status = StatusVerified
			return
		}
	}
}

// statusOf は Resolver のエラーを照合結果に畳む
func statusOf(err error) ReferenceStatus {
	if errors.Is(err, crossref.ErrNotFound) {
		return StatusNotFound
	}
	return StatusUnavailable
}
