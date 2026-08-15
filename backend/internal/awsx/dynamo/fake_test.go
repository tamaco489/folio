package dynamo

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeDynamo は API のインメモリ実装
// 条件式・更新式は文字列をそのまま解釈するため、Client が組み立てた式の意味を検証できる
type fakeDynamo struct {
	items      map[string]map[string]types.AttributeValue
	lastQuery  *dynamodb.QueryInput
	lastGet    *dynamodb.GetItemInput
	lastPut    *dynamodb.PutItemInput
	lastUpdate *dynamodb.UpdateItemInput
	err        error
}

func newFakeDynamo() *fakeDynamo {
	return &fakeDynamo{items: map[string]map[string]types.AttributeValue{}}
}

func (f *fakeDynamo) seed(job Job) {
	item, err := job.item()
	if err != nil {
		panic(err)
	}
	f.items[job.JobID] = item
}

func (f *fakeDynamo) get(jobID string) map[string]types.AttributeValue {
	return f.items[jobID]
}

func (f *fakeDynamo) PutItem(_ context.Context, params *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.lastPut = params
	if f.err != nil {
		return nil, f.err
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
		condErr := &types.ConditionalCheckFailedException{Message: aws.String("The conditional request failed")}
		if params.ReturnValuesOnConditionCheckFailure == types.ReturnValuesOnConditionCheckFailureAllOld {
			condErr.Item = copyItem(existing)
		}
		return nil, condErr
	}
	f.items[key] = copyItem(params.Item)
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeDynamo) GetItem(_ context.Context, params *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.lastGet = params
	if f.err != nil {
		return nil, f.err
	}
	key, err := keyOf(params.Key)
	if err != nil {
		return nil, err
	}
	return &dynamodb.GetItemOutput{Item: copyItem(f.items[key])}, nil
}

func (f *fakeDynamo) UpdateItem(_ context.Context, params *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.lastUpdate = params
	if f.err != nil {
		return nil, f.err
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
		return nil, &types.ConditionalCheckFailedException{Message: aws.String("The conditional request failed")}
	}
	updated := copyItem(existing)
	if err := applyUpdate(aws.ToString(params.UpdateExpression), updated, params.ExpressionAttributeNames, params.ExpressionAttributeValues); err != nil {
		return nil, err
	}
	f.items[key] = updated
	out := &dynamodb.UpdateItemOutput{}
	if params.ReturnValues == types.ReturnValueAllNew {
		out.Attributes = copyItem(updated)
	}
	return out, nil
}

func (f *fakeDynamo) Query(_ context.Context, params *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	f.lastQuery = params
	if f.err != nil {
		return nil, f.err
	}
	if aws.ToString(params.IndexName) != IndexStatusUpdatedAt {
		return nil, fmt.Errorf("fake: unknown index %q", aws.ToString(params.IndexName))
	}
	matched := make([]map[string]types.AttributeValue, 0, len(f.items))
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
	return &dynamodb.QueryOutput{Items: matched, Count: int32(len(matched))}, nil
}

func keyOf(item map[string]types.AttributeValue) (string, error) {
	key := attrString(item, attrJobID)
	if key == "" {
		return "", fmt.Errorf("fake: %s is missing", attrJobID)
	}
	return key, nil
}

func attrString(item map[string]types.AttributeValue, name string) string {
	if av, ok := item[name].(*types.AttributeValueMemberS); ok {
		return av.Value
	}
	return ""
}

func copyItem(item map[string]types.AttributeValue) map[string]types.AttributeValue {
	if item == nil {
		return nil
	}
	out := make(map[string]types.AttributeValue, len(item))
	for k, v := range item {
		out[k] = v
	}
	return out
}

// evalCondition は attribute_exists / attribute_not_exists と等値比較を AND / OR で結んだ式を評価する
func evalCondition(expr string, item map[string]types.AttributeValue, names map[string]string, values map[string]types.AttributeValue) (bool, error) {
	if strings.TrimSpace(expr) == "" {
		return true, nil
	}
	for _, orTerm := range strings.Split(expr, " OR ") {
		result := true
		for _, andTerm := range strings.Split(orTerm, " AND ") {
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

func evalTerm(term string, item map[string]types.AttributeValue, names map[string]string, values map[string]types.AttributeValue) (bool, error) {
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
func applyUpdate(expr string, item map[string]types.AttributeValue, names map[string]string, values map[string]types.AttributeValue) error {
	setPart := expr
	removePart := ""
	if idx := strings.Index(expr, " REMOVE "); idx >= 0 {
		setPart = expr[:idx]
		removePart = expr[idx+len(" REMOVE "):]
	}
	setPart = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(setPart), "SET"))
	if setPart != "" {
		for _, assign := range strings.Split(setPart, ",") {
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
	for _, target := range strings.Split(removePart, ",") {
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

func resolveValue(token string, values map[string]types.AttributeValue) (types.AttributeValue, error) {
	value, ok := values[token]
	if !ok {
		return nil, fmt.Errorf("fake: undefined value %q", token)
	}
	return value, nil
}

func attrEqual(a, b types.AttributeValue) bool {
	as, aok := a.(*types.AttributeValueMemberS)
	bs, bok := b.(*types.AttributeValueMemberS)
	return aok && bok && as.Value == bs.Value
}
