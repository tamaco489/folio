// Package dynamotest は実 API を呼ばずに awsx/dynamo とその利用側を検証するためのフェイクを提供する
//
// dynamo の内部テスト (package dynamo) からも使えるよう dynamo を import しない
// dynamo.API を満たすことの検査 (var _ dynamo.API = (*dynamotest.Fake)(nil)) は利用側に置く
package dynamotest

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awsdynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// jobs テーブルのキーと GSI (dynamo パッケージの attrJobID / attrUpdatedAt / IndexStatusUpdatedAt と揃える)
const (
	attrJobID            = "jobId"
	attrUpdatedAt        = "updatedAt"
	indexStatusUpdatedAt = "gsi-status-updatedAt"
)

// Fake はメモリ上に jobs テーブルを再現する
//
// 条件式・更新式は文字列をそのまま解釈するため、dynamo.Client が組み立てた式の意味を検証できる
// Err に値を入れると、すべての操作を失敗させられる
type Fake struct {
	items map[string]map[string]awsdynamodbtypes.AttributeValue

	Err       error
	Updates   int // Updates は UpdateItem の呼び出し回数
	LastPut   *awsdynamodb.PutItemInput
	LastGet   *awsdynamodb.GetItemInput
	LastQuery *awsdynamodb.QueryInput
}

// NewFake は空の Fake を生成する
func NewFake() *Fake {
	return &Fake{items: map[string]map[string]awsdynamodbtypes.AttributeValue{}}
}

// Seed はテストの前提となるレコードを配置する (jobId 属性をキーにする)
func (f *Fake) Seed(item map[string]awsdynamodbtypes.AttributeValue) {
	key, err := keyOf(item)
	if err != nil {
		panic(err)
	}
	f.items[key] = copyItem(item)
}

// Item は配置されているレコードのコピーを取り出す (無ければ nil)
func (f *Fake) Item(jobID string) map[string]awsdynamodbtypes.AttributeValue {
	return copyItem(f.items[jobID])
}

// Attr はレコードの文字列属性を返す (レコードか属性が無ければ空文字)
func (f *Fake) Attr(jobID, name string) string {
	return attrString(f.items[jobID], name)
}

// Len は配置されているレコード数を返す
func (f *Fake) Len() int {
	return len(f.items)
}

func (f *Fake) PutItem(_ context.Context, params *awsdynamodb.PutItemInput, _ ...func(*awsdynamodb.Options)) (*awsdynamodb.PutItemOutput, error) {
	f.LastPut = params
	if f.Err != nil {
		return nil, f.Err
	}
	key, err := keyOf(params.Item)
	if err != nil {
		return nil, err
	}
	existing := f.items[key]
	ok, err := evalCondition(aws.ToString(params.ConditionExpression), existing, params.ExpressionAttributeNames, params.ExpressionAttributeValues)
	if err != nil {
		return nil, err
	}
	if !ok {
		condErr := &awsdynamodbtypes.ConditionalCheckFailedException{Message: new("The conditional request failed")}
		if params.ReturnValuesOnConditionCheckFailure == awsdynamodbtypes.ReturnValuesOnConditionCheckFailureAllOld {
			condErr.Item = copyItem(existing)
		}
		return nil, condErr
	}
	f.items[key] = copyItem(params.Item)
	return &awsdynamodb.PutItemOutput{}, nil
}

func (f *Fake) GetItem(_ context.Context, params *awsdynamodb.GetItemInput, _ ...func(*awsdynamodb.Options)) (*awsdynamodb.GetItemOutput, error) {
	f.LastGet = params
	if f.Err != nil {
		return nil, f.Err
	}
	key, err := keyOf(params.Key)
	if err != nil {
		return nil, err
	}
	return &awsdynamodb.GetItemOutput{Item: copyItem(f.items[key])}, nil
}

func (f *Fake) UpdateItem(_ context.Context, params *awsdynamodb.UpdateItemInput, _ ...func(*awsdynamodb.Options)) (*awsdynamodb.UpdateItemOutput, error) {
	f.Updates++
	if f.Err != nil {
		return nil, f.Err
	}
	key, err := keyOf(params.Key)
	if err != nil {
		return nil, err
	}
	existing := f.items[key]
	ok, err := evalCondition(aws.ToString(params.ConditionExpression), existing, params.ExpressionAttributeNames, params.ExpressionAttributeValues)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &awsdynamodbtypes.ConditionalCheckFailedException{Message: new("The conditional request failed")}
	}
	updated := copyItem(existing)
	if err := applyUpdate(aws.ToString(params.UpdateExpression), updated, params.ExpressionAttributeNames, params.ExpressionAttributeValues); err != nil {
		return nil, err
	}
	f.items[key] = updated
	out := &awsdynamodb.UpdateItemOutput{}
	if params.ReturnValues == awsdynamodbtypes.ReturnValueAllNew {
		out.Attributes = copyItem(updated)
	}
	return out, nil
}

// Query は status ごとのジョブを updatedAt 順に引く GSI だけを再現する
func (f *Fake) Query(_ context.Context, params *awsdynamodb.QueryInput, _ ...func(*awsdynamodb.Options)) (*awsdynamodb.QueryOutput, error) {
	f.LastQuery = params
	if f.Err != nil {
		return nil, f.Err
	}
	if aws.ToString(params.IndexName) != indexStatusUpdatedAt {
		return nil, fmt.Errorf("fake: unknown index %q", aws.ToString(params.IndexName))
	}
	matched := make([]map[string]awsdynamodbtypes.AttributeValue, 0, len(f.items))
	for _, item := range f.items {
		ok, err := evalCondition(aws.ToString(params.KeyConditionExpression), item, params.ExpressionAttributeNames, params.ExpressionAttributeValues)
		if err != nil {
			return nil, err
		}
		if ok {
			matched = append(matched, copyItem(item))
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		a := attrString(matched[i], attrUpdatedAt)
		b := attrString(matched[j], attrUpdatedAt)
		if aws.ToBool(params.ScanIndexForward) {
			return a < b
		}
		return a > b
	})
	if params.Limit != nil && int(*params.Limit) < len(matched) {
		matched = matched[:*params.Limit]
	}
	return &awsdynamodb.QueryOutput{Items: matched, Count: int32(len(matched))}, nil
}

func keyOf(item map[string]awsdynamodbtypes.AttributeValue) (string, error) {
	key := attrString(item, attrJobID)
	if key == "" {
		return "", fmt.Errorf("fake: %s is missing", attrJobID)
	}
	return key, nil
}

func attrString(item map[string]awsdynamodbtypes.AttributeValue, name string) string {
	if av, ok := item[name].(*awsdynamodbtypes.AttributeValueMemberS); ok {
		return av.Value
	}
	return ""
}

func copyItem(item map[string]awsdynamodbtypes.AttributeValue) map[string]awsdynamodbtypes.AttributeValue {
	if item == nil {
		return nil
	}
	out := make(map[string]awsdynamodbtypes.AttributeValue, len(item))
	maps.Copy(out, item)
	return out
}

// evalCondition は attribute_exists / attribute_not_exists と等値比較を AND / OR で結んだ式を評価する
func evalCondition(expr string, item map[string]awsdynamodbtypes.AttributeValue, names map[string]string, values map[string]awsdynamodbtypes.AttributeValue) (bool, error) {
	if strings.TrimSpace(expr) == "" {
		return true, nil
	}
	for orTerm := range strings.SplitSeq(expr, " OR ") {
		result := true
		for andTerm := range strings.SplitSeq(orTerm, " AND ") {
			ok, err := evalTerm(strings.TrimSpace(andTerm), item, names, values)
			if err != nil {
				return false, err
			}
			if !ok {
				result = false
				break
			}
		}
		if result {
			return true, nil
		}
	}
	return false, nil
}

func evalTerm(term string, item map[string]awsdynamodbtypes.AttributeValue, names map[string]string, values map[string]awsdynamodbtypes.AttributeValue) (bool, error) {
	switch {
	case strings.HasPrefix(term, "attribute_not_exists("):
		name, err := resolveName(strings.TrimSuffix(strings.TrimPrefix(term, "attribute_not_exists("), ")"), names)
		if err != nil {
			return false, err
		}
		_, ok := item[name]
		return !ok, nil
	case strings.HasPrefix(term, "attribute_exists("):
		name, err := resolveName(strings.TrimSuffix(strings.TrimPrefix(term, "attribute_exists("), ")"), names)
		if err != nil {
			return false, err
		}
		_, ok := item[name]
		return ok, nil
	case strings.Contains(term, " = "):
		parts := strings.SplitN(term, " = ", 2)
		name, err := resolveName(strings.TrimSpace(parts[0]), names)
		if err != nil {
			return false, err
		}
		want, err := resolveValue(strings.TrimSpace(parts[1]), values)
		if err != nil {
			return false, err
		}
		got, ok := item[name]
		if !ok {
			return false, nil
		}
		return attrEqual(got, want), nil
	default:
		return false, fmt.Errorf("fake: unsupported condition term %q", term)
	}
}

// applyUpdate は "SET a = :v, b = :w REMOVE c" 形式の更新式を適用する
//
// 想定外の式は黙って無視せず落とし、フェイクが実際の更新と食い違ったまま通ることを防ぐ
func applyUpdate(expr string, item map[string]awsdynamodbtypes.AttributeValue, names map[string]string, values map[string]awsdynamodbtypes.AttributeValue) error {
	setPart := expr
	removePart := ""
	if before, after, ok := strings.Cut(expr, " REMOVE "); ok {
		setPart = before
		removePart = after
	}
	setPart = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(setPart), "SET"))
	if setPart != "" {
		for assign := range strings.SplitSeq(setPart, ",") {
			parts := strings.SplitN(assign, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("fake: unsupported assignment %q", assign)
			}
			name, err := resolveName(strings.TrimSpace(parts[0]), names)
			if err != nil {
				return err
			}
			value, err := resolveValue(strings.TrimSpace(parts[1]), values)
			if err != nil {
				return err
			}
			item[name] = value
		}
	}
	for target := range strings.SplitSeq(removePart, ",") {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		name, err := resolveName(target, names)
		if err != nil {
			return err
		}
		delete(item, name)
	}
	return nil
}

func resolveName(token string, names map[string]string) (string, error) {
	if !strings.HasPrefix(token, "#") {
		return token, nil
	}
	name, ok := names[token]
	if !ok {
		return "", fmt.Errorf("fake: undefined name %q", token)
	}
	return name, nil
}

func resolveValue(token string, values map[string]awsdynamodbtypes.AttributeValue) (awsdynamodbtypes.AttributeValue, error) {
	value, ok := values[token]
	if !ok {
		return nil, fmt.Errorf("fake: undefined value %q", token)
	}
	return value, nil
}

func attrEqual(a, b awsdynamodbtypes.AttributeValue) bool {
	as, aok := a.(*awsdynamodbtypes.AttributeValueMemberS)
	bs, bok := b.(*awsdynamodbtypes.AttributeValueMemberS)
	return aok && bok && as.Value == bs.Value
}
