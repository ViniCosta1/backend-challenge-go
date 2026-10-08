#!/bin/sh
set -eu

# MiniStack runs ready.d after the gateway is available. Also compatible with
# LocalStack when mounted under /etc/localstack/init/ready.d.
export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-test}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-test}"
export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-us-east-1}"
export AWS_PAGER=""
sqs_endpoint="${AWS_ENDPOINT_URL:-http://localhost:4566}"
max_receive_count="${SQS_MAX_RECEIVE_COUNT:-5}"
case "$max_receive_count" in
    ''|*[!0-9]*) echo "SQS_MAX_RECEIVE_COUNT must be a positive integer" >&2; exit 1 ;;
esac
if [ "$max_receive_count" -lt 1 ]; then
    echo "SQS_MAX_RECEIVE_COUNT must be a positive integer" >&2
    exit 1
fi

ensure_queue() {
    queue_name="$1"
    aws --endpoint-url "$sqs_endpoint" sqs get-queue-url --queue-name "$queue_name" --query QueueUrl --output text 2>/dev/null ||
        aws --endpoint-url "$sqs_endpoint" sqs create-queue --queue-name "$queue_name" \
            --attributes '{"FifoQueue":"true","ContentBasedDeduplication":"false"}' --query QueueUrl --output text
}

dlq_url=$(ensure_queue "${SQS_DLQ_QUEUE_NAME:-wager-transactions-dlq.fifo}")
input_url=$(ensure_queue "${SQS_INPUT_QUEUE_NAME:-wager-transactions.fifo}")
events_url=$(ensure_queue "${SQS_EVENTS_QUEUE_NAME:-wager-events.fifo}")
dlq_arn=$(aws --endpoint-url "$sqs_endpoint" sqs get-queue-attributes --queue-url "$dlq_url" \
    --attribute-names QueueArn --query Attributes.QueueArn --output text)

aws --endpoint-url "$sqs_endpoint" sqs set-queue-attributes --queue-url "$dlq_url" \
    --attributes '{"MessageRetentionPeriod":"1209600","VisibilityTimeout":"30"}'
aws --endpoint-url "$sqs_endpoint" sqs set-queue-attributes --queue-url "$events_url" \
    --attributes '{"MessageRetentionPeriod":"345600","VisibilityTimeout":"30","ReceiveMessageWaitTimeSeconds":"20"}'
# All inputs to this JSON are fixed or returned by SQS, not untrusted payloads.
input_attributes='{"VisibilityTimeout":"30","ReceiveMessageWaitTimeSeconds":"20","MessageRetentionPeriod":"345600","RedrivePolicy":"{\"deadLetterTargetArn\":\"'"$dlq_arn"'\",\"maxReceiveCount\":\"'"$max_receive_count"'\"}"}'
aws --endpoint-url "$sqs_endpoint" sqs set-queue-attributes --queue-url "$input_url" --attributes "$input_attributes"
