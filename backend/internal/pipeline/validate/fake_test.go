package validate

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
	"github.com/tamaco489/folio/backend/internal/pipeline/pdf"
)

// seededAt は dynamo が用いる固定桁のタイムスタンプ表現に合わせた既存レコードの時刻
const seededAt = "2026-08-15T10:00:00.000000000Z"

var _ dynamo.API = (*fakeDynamo)(nil)

// fakeDynamo は dynamo.API のインメモリ実装
//
// 条件式と更新式の組み立て自体は dynamo パッケージのテストが検証済みのため、ここでは RegisterJob と MarkFailed が依存する意味だけを再現する
type fakeDynamo struct {
	items map[string]map[string]types.AttributeValue
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

func (f *fakeDynamo) exists(jobID string) bool {
	_, ok := f.items[jobID]
	return ok
}

func (f *fakeDynamo) PutItem(_ context.Context, params *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	jobID := stringAttr(params.Item, "jobId")
	existing, ok := f.items[jobID]
	if ok && stringAttr(existing, "status") != string(dynamo.StatusFailed) {
		return nil, &types.ConditionalCheckFailedException{
			Message: aws.String("The conditional request failed"),
			Item:    existing,
		}
	}
	f.items[jobID] = params.Item
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeDynamo) UpdateItem(_ context.Context, params *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	jobID := stringAttr(params.Key, "jobId")
	item, ok := f.items[jobID]
	if !ok {
		return nil, &types.ConditionalCheckFailedException{Message: aws.String("The conditional request failed")}
	}
	if err := applySet(aws.ToString(params.UpdateExpression), item, params.ExpressionAttributeNames, params.ExpressionAttributeValues); err != nil {
		return nil, err
	}
	return &dynamodb.UpdateItemOutput{Attributes: item}, nil
}

func (f *fakeDynamo) GetItem(_ context.Context, params *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{Item: f.items[stringAttr(params.Key, "jobId")]}, nil
}

func (f *fakeDynamo) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return nil, fmt.Errorf("fake: validate does not query the jobs table")
}

// applySet は "SET #a = :v, #b = :w" 形式の更新式だけを適用する
//
// 想定外の式は黙って無視せず落とし、フェイクが実際の更新と食い違ったまま通ることを防ぐ
func applySet(expr string, item map[string]types.AttributeValue, names map[string]string, values map[string]types.AttributeValue) error {
	assignments, ok := strings.CutPrefix(strings.TrimSpace(expr), "SET ")
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
	return nil
}

func stringAttr(item map[string]types.AttributeValue, name string) string {
	if av, ok := item[name].(*types.AttributeValueMemberS); ok {
		return av.Value
	}
	return ""
}

var _ PDFInfo = (*fakePDF)(nil)

// fakePDF は poppler を呼ばずに pdfinfo の結果を差し替える
type fakePDF struct {
	info pdf.Info
	err  error
}

func (f *fakePDF) Info(context.Context, string) (pdf.Info, error) {
	return f.info, f.err
}

var _ s3.API = (*oversizeS3)(nil)

// oversizeS3 は HeadObject が報告するサイズだけを差し替える
//
// 500MB の実データを用意せずに上限超過を検証するために使う
type oversizeS3 struct {
	s3.API
	size int64
}

func (o *oversizeS3) HeadObject(ctx context.Context, params *awss3.HeadObjectInput, optFns ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error) {
	out, err := o.API.HeadObject(ctx, params, optFns...)
	if err != nil {
		return nil, err
	}
	out.ContentLength = aws.Int64(o.size)
	return out, nil
}
