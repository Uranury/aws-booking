# bookinapp Makefile
#
# `sam build` calls the build-<LogicalId> targets below (BuildMethod: makefile)
# and passes ARTIFACTS_DIR. Each target compiles one cmd/<name> package into a
# single static binary called `bootstrap`, which is what provided.al2023 runs.
#
# Other targets:
#   make test        run unit tests
#   make build-all   build every Lambda into bin/<name>/bootstrap (no SAM needed)
#   make backup      take an on-demand DynamoDB backup
#   make seed        load sample events and seats
#   make deploy-web  upload web/ to S3 and invalidate CloudFront

STACK_NAME ?= bookinapp
GO_BUILD = GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -trimpath -ldflags="-s -w"

# Every Lambda: <SAM logical id>:<directory under cmd/>
LAMBDAS := \
	SeedFunction:seed \
	GetEventsFunction:get-events \
	GetEventFunction:get-event \
	CreateBookingFunction:create-booking \
	GetBookingFunction:get-booking

define build_lambda
	$(GO_BUILD) -o $(ARTIFACTS_DIR)/bootstrap ./cmd/$(1)
endef

build-SeedFunction:
	$(call build_lambda,seed)
build-GetEventsFunction:
	$(call build_lambda,get-events)
build-GetEventFunction:
	$(call build_lambda,get-event)
build-CreateBookingFunction:
	$(call build_lambda,create-booking)
build-GetBookingFunction:
	$(call build_lambda,get-booking)

# Reads one output value from the deployed CloudFormation stack.
stack_output = $(shell aws cloudformation describe-stacks --stack-name $(STACK_NAME) \
	--query "Stacks[0].Outputs[?OutputKey=='$(1)'].OutputValue" --output text)

.PHONY: test build-all clean backup seed deploy-web

test:
	go test ./...

build-all:
	@for pair in $(LAMBDAS); do \
		dir=$${pair#*:}; \
		echo "building $$dir"; \
		$(GO_BUILD) -o bin/$$dir/bootstrap ./cmd/$$dir || exit 1; \
	done

clean:
	rm -rf bin .aws-sam

# On-demand backup. Point-in-time recovery is also enabled on the table, so this
# is for named snapshots you want to keep (e.g. before a demo or a data change).
backup:
	aws dynamodb create-backup \
		--table-name $(call stack_output,TableName) \
		--backup-name $(STACK_NAME)-$$(date +%Y%m%d-%H%M%S)

# Writes the result to a file (not /dev/stdout) so it also works with the
# Windows AWS CLI called from WSL.
seed:
	aws lambda invoke --function-name $(call stack_output,SeedFunctionName) \
		--cli-binary-format raw-in-base64-out --payload '{}' seed-output.json
	@cat seed-output.json; echo; rm -f seed-output.json

# The bucket is private, so files are uploaded with your AWS credentials and
# served only through CloudFront. The invalidation makes changes show up at once.
deploy-web:
	aws s3 sync web/ s3://$(call stack_output,WebBucketName) --delete
	aws cloudfront create-invalidation --distribution-id $(call stack_output,DistributionId) --paths '/*'
