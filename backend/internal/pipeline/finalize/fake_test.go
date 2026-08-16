package finalize

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/tamaco489/folio/backend/internal/awsx/dynamo"
	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/awsx/s3/s3test"
)

// seededAt は dynamo が用いる固定桁のタイムスタンプ表現に合わせた既存レコードの時刻
const seededAt = "2026-08-16T00:00:00.000000000Z"

var _ dynamo.API = (*fakeDynamo)(nil)

// fakeDynamo は dynamo.API のインメモリ実装
//
// 条件式と更新式の組み立て自体は dynamo パッケージのテストが検証済みのため、UpdateStatus と MarkFailed が依存する意味 (既存レコードの条件、SET と REMOVE) だけを再現する
// RegisterJob と一覧は finalizer が呼ばないため PutItem GetItem Query は未実装で panic させる
type fakeDynamo struct {
	items   map[string]map[string]types.AttributeValue
	updates int   // updates は UpdateItem の呼び出し回数
	err     error // err を設定すると UpdateItem が失敗する
}

func newFakeDynamo() *fakeDynamo {
	return &fakeDynamo{items: map[string]map[string]types.AttributeValue{}}
}

// seed は既存ジョブを配置する
func (f *fakeDynamo) seed(jobID string, status dynamo.Status) {
	f.items[jobID] = map[string]types.AttributeValue{
		"jobId":     &types.AttributeValueMemberS{Value: jobID},
		"status":    &types.AttributeValueMemberS{Value: string(status)},
		"filename":  &types.AttributeValueMemberS{Value: "seeded.pdf"},
		"createdAt": &types.AttributeValueMemberS{Value: seededAt},
		"updatedAt": &types.AttributeValueMemberS{Value: seededAt},
	}
}

func (f *fakeDynamo) attr(jobID, name string) string {
	return stringAttr(f.items[jobID], name)
}

func (f *fakeDynamo) has(jobID, name string) bool {
	_, ok := f.items[jobID][name]
	return ok
}

func (f *fakeDynamo) UpdateItem(_ context.Context, params *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updates++
	if f.err != nil {
		return nil, f.err
	}
	jobID := stringAttr(params.Key, "jobId")
	item, ok := f.items[jobID]
	if !ok {
		return nil, &types.ConditionalCheckFailedException{Message: aws.String("The conditional request failed")}
	}
	if err := applyUpdate(aws.ToString(params.UpdateExpression), item, params.ExpressionAttributeNames, params.ExpressionAttributeValues); err != nil {
		return nil, err
	}
	return &dynamodb.UpdateItemOutput{Attributes: item}, nil
}

func (f *fakeDynamo) PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	panic("fake: finalize does not register jobs")
}

func (f *fakeDynamo) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	panic("fake: finalize does not get jobs")
}

func (f *fakeDynamo) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	panic("fake: finalize does not query the jobs table")
}

// applyUpdate は "SET #a = :v, #b = :w REMOVE #c" 形式の更新式を適用する
//
// 想定外の式は黙って無視せず落とし、フェイクが実際の更新と食い違ったまま通ることを防ぐ
func applyUpdate(expr string, item map[string]types.AttributeValue, names map[string]string, values map[string]types.AttributeValue) error {
	setPart, removePart, _ := strings.Cut(expr, " REMOVE ")
	assignments, ok := strings.CutPrefix(strings.TrimSpace(setPart), "SET ")
	if !ok {
		return fmt.Errorf("fake: unsupported update expression %q", expr)
	}
	for _, assignment := range strings.Split(assignments, ",") {
		nameToken, valueToken, ok := strings.Cut(assignment, "=")
		if !ok {
			return fmt.Errorf("fake: unsupported assignment %q", assignment)
		}
		name, ok := names[strings.TrimSpace(nameToken)]
		if !ok {
			return fmt.Errorf("fake: undefined name %q", nameToken)
		}
		value, ok := values[strings.TrimSpace(valueToken)]
		if !ok {
			return fmt.Errorf("fake: undefined value %q", valueToken)
		}
		item[name] = value
	}
	for _, target := range strings.Split(removePart, ",") {
		if target = strings.TrimSpace(target); target == "" {
			continue
		}
		name, ok := names[target]
		if !ok {
			return fmt.Errorf("fake: undefined name %q", target)
		}
		delete(item, name)
	}
	return nil
}

func stringAttr(item map[string]types.AttributeValue, name string) string {
	if av, ok := item[name].(*types.AttributeValueMemberS); ok {
		return av.Value
	}
	return ""
}

var _ s3.API = (*spyS3)(nil)

// spyS3 は s3test.Fake に委譲しつつ GetObject のキーを記録する (読みに行かないことを確かめるため)
type spyS3 struct {
	*s3test.Fake
	gets []string
}

func (s *spyS3) GetObject(ctx context.Context, params *awss3.GetObjectInput, optFns ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
	s.gets = append(s.gets, aws.ToString(params.Key))
	return s.Fake.GetObject(ctx, params, optFns...)
}
