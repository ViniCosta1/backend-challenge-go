package sqs

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

type QueueConfig struct{ Name, URL string }
type Config struct {
	Endpoint, Region, AccessKey, SecretKey string
	SessionToken                           string
	Input, DLQ, Events                     QueueConfig
}
type Queues struct{ Input, DLQ, Events string }

func NewClient(ctx context.Context, settings Config) (*awssqs.Client, error) {
	if settings.Region == "" || (settings.AccessKey == "") != (settings.SecretKey == "") {
		return nil, fmt.Errorf("SQS region and a complete credentials pair are required")
	}
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(settings.Region),
		awsconfig.WithHTTPClient(&http.Client{Timeout: 25 * time.Second}), awsconfig.WithRetryMaxAttempts(1)}
	if settings.AccessKey != "" {
		options = append(options, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(settings.AccessKey, settings.SecretKey, settings.SessionToken)))
	}
	config, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return awssqs.NewFromConfig(config, func(options *awssqs.Options) {
		if settings.Endpoint != "" {
			options.BaseEndpoint = aws.String(settings.Endpoint)
		}
	}), nil
}

func ResolveQueues(ctx context.Context, client *awssqs.Client, config Config) (Queues, error) {
	var queues Queues
	for _, item := range []struct {
		settings QueueConfig
		result   *string
	}{{config.Input, &queues.Input}, {config.DLQ, &queues.DLQ}, {config.Events, &queues.Events}} {
		if item.settings.URL != "" {
			*item.result = item.settings.URL
			continue
		}
		response, err := client.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String(item.settings.Name)})
		if err != nil {
			return Queues{}, fmt.Errorf("resolve SQS queue %q: %w: %v", item.settings.Name, application.ErrUnavailable, err)
		}
		*item.result = aws.ToString(response.QueueUrl)
	}
	return queues, nil
}

type ReadinessChecker struct {
	client *awssqs.Client
	config Config
}

func NewReadinessChecker(client *awssqs.Client, config Config) *ReadinessChecker {
	return &ReadinessChecker{client, config}
}
func (c *ReadinessChecker) Check(ctx context.Context) error {
	queues, err := ResolveQueues(ctx, c.client, c.config)
	if err != nil {
		return err
	}
	for _, queue := range []string{queues.Input, queues.DLQ, queues.Events} {
		response, err := c.client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: aws.String(queue), AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn, types.QueueAttributeNameFifoQueue}})
		if err != nil {
			return fmt.Errorf("check SQS queue: %w: %v", application.ErrUnavailable, err)
		}
		if response.Attributes[string(types.QueueAttributeNameQueueArn)] == "" || response.Attributes[string(types.QueueAttributeNameFifoQueue)] != "true" {
			return fmt.Errorf("SQS queue must be accessible and FIFO")
		}
	}
	return nil
}

type EventSender struct {
	client   *awssqs.Client
	queueURL string
}

func NewEventSender(client *awssqs.Client, queueURL string) *EventSender {
	return &EventSender{client, queueURL}
}
func (s *EventSender) Publish(ctx context.Context, event *domain.OutboxEvent) error {
	_, err := s.client.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: aws.String(s.queueURL),
		MessageBody: aws.String(string(event.Payload())), MessageGroupId: aws.String(event.AggregateID()), MessageDeduplicationId: aws.String(event.EventID())})
	if err != nil {
		return fmt.Errorf("send integration event: %w: %v", application.ErrUnavailable, err)
	}
	return nil
}

var _ application.IntegrationEventPublisher = (*EventSender)(nil)
