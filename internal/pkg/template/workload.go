// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package template

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/dustin/go-humanize/english"

	"github.com/google/uuid"

	"github.com/aws/aws-sdk-go/aws"
)

// Constants for template paths.
const (
	// Paths of workload cloudformation templates under templates/workloads/.
	fmtWkldCFTemplatePath         = "workloads/%s/%s/cf.yml"
	fmtWkldPartialsCFTemplatePath = "workloads/partials/cf/%s.yml"

	// Directories under templates/workloads/.
	servicesDirName = "services"
	jobDirName      = "jobs"

	// Names of workload templates.
	lbWebSvcTplName     = "lb-web"
	rdWebSvcTplName     = "rd-web"
	backendSvcTplName   = "backend"
	workerSvcTplName    = "worker"
	scheduledJobTplName = "scheduled-job"
)

// Constants for workload options.
const (
	// AWS VPC networking configuration.
	EnablePublicIP          = "ENABLED"
	DisablePublicIP         = "DISABLED"
	PublicSubnetsPlacement  = "PublicSubnets"
	PrivateSubnetsPlacement = "PrivateSubnets"

	// RuntimePlatform configuration.
	OSLinux             = "LINUX"
	OSWindowsServerFull = "WINDOWS_SERVER_2019_FULL"
	OSWindowsServerCore = "WINDOWS_SERVER_2019_CORE"

	ArchX86   = "X86_64"
	ArchARM   = "ARM"
	ArchARM64 = "ARM64"
)

// Constants for ARN options.
const (
	snsARNPattern = "arn:%s:sns:%s:%s:%s-%s-%s-%s"
)

var (
	// Template names under "workloads/partials/cf/".
	partialsWorkloadCFTemplateNames = []string{
		"loggroup",
		"envvars-container",
		"envvars-common",
		"secrets",
		"executionrole",
		"taskrole",
		"workload-container",
		"fargate-taskdef-base-properties",
		"service-base-properties",
		"servicediscovery",
		"addons",
		"sidecars",
		"logconfig",
		"autoscaling",
		"eventrule",
		"state-machine",
		"state-machine-definition.json",
		"efs-access-point",
		"env-controller",
		"mount-points",
		"volumes",
		"image-overrides",
		"instancerole",
		"accessrole",
		"publish",
		"subscribe",
		"nlb",
		"vpc-connector",
	}

	// Operating systems to determine Fargate platform versions.
	osFamiliesForPV100 = []string{
		OSWindowsServerFull, OSWindowsServerCore,
	}
)

// WorkloadNestedStackOpts holds configuration that's needed if the workload stack has a nested stack.
type WorkloadNestedStackOpts struct {
	StackName string

	VariableOutputs      []string
	SecretOutputs        []string
	PolicyOutputs        []string
	SecurityGroupOutputs []string
}

// SidecarOpts holds configuration that's needed if the service has sidecar containers.
type SidecarOpts struct {
	Name         *string
	Image        *string
	Essential    *bool
	Port         *string
	Protocol     *string
	CredsParam   *string
	Variables    map[string]string
	Secrets      map[string]string
	Storage      SidecarStorageOpts
	DockerLabels map[string]string
	DependsOn    map[string]string
	EntryPoint   []string
	Command      []string
	HealthCheck  *ContainerHealthCheck
}

// SidecarStorageOpts holds data structures for rendering Mount Points inside of a sidecar.
type SidecarStorageOpts struct {
	MountPoints []*MountPoint
}

// StorageOpts holds data structures for rendering Volumes and Mount Points
type StorageOpts struct {
	Ephemeral         *int
	Volumes           []*Volume
	MountPoints       []*MountPoint
	EFSPerms          []*EFSPermission
	ManagedVolumeInfo *ManagedVolumeCreationInfo // Used for delegating CreationInfo for Copilot-managed EFS.
}

// requiresEFSCreation returns true if managed volume information is specified; false otherwise.
func (s *StorageOpts) requiresEFSCreation() bool {
	return s.ManagedVolumeInfo != nil
}

// EFSPermission holds information needed to render an IAM policy statement.
type EFSPermission struct {
	FilesystemID  *string
	Write         bool
	AccessPointID *string
}

// MountPoint holds information needed to render a MountPoint in a containerdefinition.
type MountPoint struct {
	ContainerPath *string
	ReadOnly      *bool
	SourceVolume  *string
}

// Volume contains fields that render a volume, its name, and EFSVolumeConfiguration
type Volume struct {
	Name *string

	EFS *EFSVolumeConfiguration
}

// ManagedVolumeCreationInfo holds information about how to create Copilot-managed access points.
type ManagedVolumeCreationInfo struct {
	Name    *string
	DirName *string
	UID     *uint32
	GID     *uint32
}

// EFSVolumeConfiguration contains information about how to specify externally managed file systems.
type EFSVolumeConfiguration struct {
	// EFSVolumeConfiguration
	Filesystem    *string
	RootDirectory *string // "/" or empty are equivalent

	// Authorization Config
	AccessPointID *string
	IAM           *string // ENABLED or DISABLED
}

// LogConfigOpts holds configuration that's needed if the service is configured with Firelens to route
// its logs.
type LogConfigOpts struct {
	Image          *string
	Destination    map[string]string
	EnableMetadata *string
	SecretOptions  map[string]string
	ConfigFile     *string
	Variables      map[string]string
	Secrets        map[string]string
}

// HTTPHealthCheckOpts holds configuration that's needed for HTTP Health Check.
type HTTPHealthCheckOpts struct {
	HealthCheckPath     string
	SuccessCodes        string
	HealthyThreshold    *int64
	UnhealthyThreshold  *int64
	Interval            *int64
	Timeout             *int64
	DeregistrationDelay *int64
	GracePeriod         *int64
}

// NetworkLoadBalancerListener holds configuration that's need for a Network Load Balancer listener.
type NetworkLoadBalancerListener struct {
	Port            string
	Protocol        string
	TargetContainer string
	TargetPort      string
	SSLPolicy       *string
	Aliases         []string
}

// NetworkLoadBalancer holds configuration that's needed for a Network Load Balancer.
type NetworkLoadBalancer struct {
	PublicSubnetCIDRs []string
	Listener          NetworkLoadBalancerListener
	MainContainerPort string
}

// AdvancedCount holds configuration for autoscaling and capacity provider
// parameters.
type AdvancedCount struct {
	Spot        *int
	Autoscaling *AutoscalingOpts
	Cps         []*CapacityProviderStrategy
}

// ContainerHealthCheck holds configuration for container health check.
type ContainerHealthCheck struct {
	Command     []string
	Interval    *int64
	Retries     *int64
	StartPeriod *int64
	Timeout     *int64
}

// CapacityProviderStrategy holds the configuration needed for a
// CapacityProviderStrategyItem on a Service
type CapacityProviderStrategy struct {
	Base             *int
	Weight           *int
	CapacityProvider string
}

// AutoscalingOpts holds configuration that's needed for Auto Scaling.
type AutoscalingOpts struct {
	MinCapacity  *int
	MaxCapacity  *int
	CPU          *float64
	Memory       *float64
	Requests     *float64
	ResponseTime *float64
	QueueDelay   *AutoscalingQueueDelayOpts
}

// AutoscalingQueueDelayOpts holds configuration to scale SQS queues.
type AutoscalingQueueDelayOpts struct {
	AcceptableBacklogPerTask int
}

// ExecuteCommandOpts holds configuration that's needed for ECS Execute Command.
type ExecuteCommandOpts struct{}

// StateMachineOpts holds configuration needed for State Machine retries and timeout.
type StateMachineOpts struct {
	Timeout *int
	Retries *int
}

// PublishOpts holds configuration needed if the service has publishers.
type PublishOpts struct {
	Topics []*Topic
}

// Topic holds information needed to render a SNSTopic in a container definition.
type Topic struct {
	Name *string

	Region    string
	Partition string
	AccountID string
	App       string
	Env       string
	Svc       string
}

// SubscribeOpts holds configuration needed if the service has subscriptions.
type SubscribeOpts struct {
	Topics []*TopicSubscription
	Queue  *SQSQueue
}

// HasTopicQueues returns true if any individual subscription has a dedicated queue.
func (s *SubscribeOpts) HasTopicQueues() bool {
	for _, t := range s.Topics {
		if t.Queue != nil {
			return true
		}
	}
	return false
}

// TopicSubscription holds information needed to render a SNS Topic Subscription in a container definition.
type TopicSubscription struct {
	Name    *string
	Service *string
	Queue   *SQSQueue
}

// SQSQueue holds information needed to render a SQS Queue in a container definition.
type SQSQueue struct {
	Retention  *int64
	Delay      *int64
	Timeout    *int64
	DeadLetter *DeadLetterQueue
}

// DeadLetterQueue holds information needed to render a dead-letter SQS Queue in a container definition.
type DeadLetterQueue struct {
	Tries *uint16
}

// NetworkOpts holds AWS networking configuration for the workloads.
type NetworkOpts struct {
	AssignPublicIP string
	SubnetsType    string
	SecurityGroups []string
}

// RuntimePlatformOpts holds configuration needed for Platform configuration.
type RuntimePlatformOpts struct {
	OS   string
	Arch string
}

// IsDefault returns true if the platform matches the default docker image platform of "linux/amd64".
func (p RuntimePlatformOpts) IsDefault() bool {
	if p.isEmpty() {
		return true
	}
	if p.OS == OSLinux && p.Arch == ArchX86 {
		return true
	}
	return false
}

// Version returns the Fargate platform version based on the selected os family.
func (p RuntimePlatformOpts) Version() string {
	for _, os := range osFamiliesForPV100 {
		if p.OS == os {
			return "1.0.0"
		}
	}
	return "LATEST"
}

func (p RuntimePlatformOpts) isEmpty() bool {
	return p.OS == "" && p.Arch == ""
}

// WorkloadOpts holds optional data that can be provided to enable features in a workload stack template.
type WorkloadOpts struct {
	// Additional options that are common between **all** workload templates.
	Variables                map[string]string
	Secrets                  map[string]string
	Aliases                  []string
	Tags                     map[string]string        // Used by App Runner workloads to tag App Runner service resources
	NestedStack              *WorkloadNestedStackOpts // Outputs from nested stacks such as the addons stack.
	AddonsExtraParams        string                   // Additional user defined Parameters for the addons stack.
	Sidecars                 []*SidecarOpts
	LogConfig                *LogConfigOpts
	Autoscaling              *AutoscalingOpts
	CapacityProviders        []*CapacityProviderStrategy
	DesiredCountOnSpot       *int
	Storage                  *StorageOpts
	Network                  NetworkOpts
	ExecuteCommand           *ExecuteCommandOpts
	Platform                 RuntimePlatformOpts
	EntryPoint               []string
	Command                  []string
	DomainAlias              string
	DockerLabels             map[string]string
	DependsOn                map[string]string
	Publish                  *PublishOpts
	ServiceDiscoveryEndpoint string
	HTTPVersion              *string

	// Additional options for service templates.
	WorkloadType        string
	HealthCheck         *ContainerHealthCheck
	HTTPHealthCheck     HTTPHealthCheckOpts
	DeregistrationDelay *int64
	AllowedSourceIps    []string
	NLB                 *NetworkLoadBalancer

	// Lambda functions.
	RulePriorityLambda             string
	DesiredCountLambda             string
	EnvControllerLambda            string
	CredentialsParameter           string
	BacklogPerTaskCalculatorLambda string
	NLBCertManagerFunctionLambda   string

	// Additional options for job templates.
	ScheduleExpression string
	StateMachine       *StateMachineOpts

	// Additional options for request driven web service templates.
	StartCommand      *string
	EnableHealthCheck bool
	// Input needed for the custom resource that adds a custom domain to the service.
	Alias                *string
	ScriptBucketName     *string
	CustomDomainLambda   *string
	AWSSDKLayer          *string
	AppDNSDelegationRole *string
	AppDNSName           *string

	// Additional options for worker service templates.
	Subscribe *SubscribeOpts

	// List of features to enable for testing that are not yet released.
	FeatureFlags []string
}

// ParseLoadBalancedWebService parses a load balanced web service's CloudFormation template
// with the specified data object and returns its content.
func (t *Template) ParseLoadBalancedWebService(data WorkloadOpts) (*Content, error) {
	return t.parseSvc(lbWebSvcTplName, data, withSvcParsingFuncs())
}

// ParseRequestDrivenWebService parses a request-driven web service's CloudFormation template
// with the specified data object and returns its content.
func (t *Template) ParseRequestDrivenWebService(data WorkloadOpts) (*Content, error) {
	return t.parseSvc(rdWebSvcTplName, data, withSvcParsingFuncs())
}

// ParseBackendService parses a backend service's CloudFormation template with the specified data object and returns its content.
func (t *Template) ParseBackendService(data WorkloadOpts) (*Content, error) {
	return t.parseSvc(backendSvcTplName, data, withSvcParsingFuncs())
}

// ParseWorkerService parses a worker service's CloudFormation template with the specified data object and returns its content.
func (t *Template) ParseWorkerService(data WorkloadOpts) (*Content, error) {
	return t.parseSvc(workerSvcTplName, data, withSvcParsingFuncs())
}

// ParseScheduledJob parses a scheduled job's Cloudformation Template
func (t *Template) ParseScheduledJob(data WorkloadOpts) (*Content, error) {
	return t.parseJob(scheduledJobTplName, data, withSvcParsingFuncs())
}

// parseSvc parses a service's CloudFormation template with the specified data object and returns its content.
func (t *Template) parseSvc(name string, data interface{}, options ...ParseOption) (*Content, error) {
	return t.parseWkld(name, servicesDirName, data, options...)
}

// parseJob parses a job's Cloudformation template with the specified data object and returns its content.
func (t *Template) parseJob(name string, data interface{}, options ...ParseOption) (*Content, error) {
	return t.parseWkld(name, jobDirName, data, options...)
}

func (t *Template) parseWkld(name, wkldDirName string, data interface{}, options ...ParseOption) (*Content, error) {
	tpl, err := t.parse("base", fmt.Sprintf(fmtWkldCFTemplatePath, wkldDirName, name), options...)
	if err != nil {
		return nil, err
	}
	for _, templateName := range partialsWorkloadCFTemplateNames {
		nestedTpl, err := t.parse(templateName, fmt.Sprintf(fmtWkldPartialsCFTemplatePath, templateName), options...)
		if err != nil {
			return nil, err
		}
		_, err = tpl.AddParseTree(templateName, nestedTpl.Tree)
		if err != nil {
			return nil, fmt.Errorf("add parse tree of %s to base template: %w", templateName, err)
		}
	}
	buf := &bytes.Buffer{}
	if err := tpl.Execute(buf, data); err != nil {
		return nil, fmt.Errorf("execute template %s with data %v: %w", name, data, err)
	}
	return &Content{buf}, nil
}

func withSvcParsingFuncs() ParseOption {
	return func(t *template.Template) *template.Template {
		return t.Funcs(map[string]interface{}{
			"toSnakeCase":         ToSnakeCaseFunc,
			"hasSecrets":          hasSecrets,
			"fmtSlice":            FmtSliceFunc,
			"quoteSlice":          QuoteSliceFunc,
			"randomUUID":          randomUUIDFunc,
			"jsonMountPoints":     generateMountPointJSON,
			"jsonSNSTopics":       generateSNSJSON,
			"jsonQueueURIs":       generateQueueURIJSON,
			"envControllerParams": envControllerParameters,
			"logicalIDSafe":       StripNonAlphaNumFunc,
			"wordSeries":          english.WordSeries,
			"pluralWord":          english.PluralWord,
			"contains":            contains,
		})
	}
}

func hasSecrets(opts WorkloadOpts) bool {
	if len(opts.Secrets) > 0 {
		return true
	}
	if opts.NestedStack != nil && (len(opts.NestedStack.SecretOutputs) > 0) {
		return true
	}
	return false
}

func randomUUIDFunc() (string, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("generate random uuid: %w", err)
	}
	return id.String(), err
}

// envControllerParameters determines which parameters to include in the EnvController template.
func envControllerParameters(o WorkloadOpts) []string {
	parameters := []string{}
	if o.WorkloadType == "Load Balanced Web Service" {
		parameters = append(parameters, []string{"ALBWorkloads,", "Aliases,"}...) // YAML needs the comma separator; resolved in EnvContr.
	}
	if o.Network.SubnetsType == PrivateSubnetsPlacement {
		parameters = append(parameters, "NATWorkloads,")
	}
	if o.Storage != nil && o.Storage.requiresEFSCreation() {
		parameters = append(parameters, "EFSWorkloads,")
	}
	return parameters
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// ARN determines the arn for a topic using the SNSTopic name and account information
func (t Topic) ARN() string {
	return fmt.Sprintf(snsARNPattern, t.Partition, t.Region, t.AccountID, t.App, t.Env, t.Svc, aws.StringValue(t.Name))
}
