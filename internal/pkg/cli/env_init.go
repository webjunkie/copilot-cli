// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/copilot-cli/internal/pkg/aws/cloudformation"
	"github.com/aws/copilot-cli/internal/pkg/aws/ec2"
	"github.com/aws/copilot-cli/internal/pkg/aws/iam"
	"github.com/aws/copilot-cli/internal/pkg/aws/identity"
	"github.com/aws/copilot-cli/internal/pkg/aws/profile"
	"github.com/aws/copilot-cli/internal/pkg/aws/s3"
	"github.com/aws/copilot-cli/internal/pkg/aws/sessions"
	"github.com/aws/copilot-cli/internal/pkg/config"
	"github.com/aws/copilot-cli/internal/pkg/deploy"
	deploycfn "github.com/aws/copilot-cli/internal/pkg/deploy/cloudformation"
	"github.com/aws/copilot-cli/internal/pkg/deploy/cloudformation/stack"
	"github.com/aws/copilot-cli/internal/pkg/template"
	"github.com/aws/copilot-cli/internal/pkg/term/color"
	"github.com/aws/copilot-cli/internal/pkg/term/log"
	termprogress "github.com/aws/copilot-cli/internal/pkg/term/progress"
	"github.com/aws/copilot-cli/internal/pkg/term/prompt"
	"github.com/aws/copilot-cli/internal/pkg/term/selector"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	envInitAppNameHelpPrompt = "An environment will be created in the selected application."

	envInitNamePrompt              = "What is your environment's name?"
	envInitNameHelpPrompt          = "A unique identifier for an environment (e.g. dev, test, prod)."
	envInitDefaultEnvConfirmPrompt = `Would you like to use the default configuration for a new environment?
    - A new VPC with 2 AZs, 2 public subnets and 2 private subnets
    - A new ECS Cluster
    - New IAM Roles to manage services and jobs in your environment
`
	envInitVPCSelectPrompt            = "Which VPC would you like to use?"
	envInitPublicSubnetsSelectPrompt  = "Which public subnets would you like to use?\nYou may choose to press 'Enter' to skip this step if the services and/or jobs you'll deploy to this environment are not internet-facing."
	envInitPrivateSubnetsSelectPrompt = "Which private subnets would you like to use?"

	envInitVPCCIDRPrompt         = "What VPC CIDR would you like to use?"
	envInitVPCCIDRPromptHelp     = "CIDR used for your VPC. For example: 10.1.0.0/16"
	envInitAdjustAZPrompt        = "Which availability zones would you like to use?"
	envInitAdjustAZPromptHelp    = "Availability zone names that span your resources. For example: us-east-1a,us-east1b,us-east-1c"
	envInitPublicCIDRPrompt      = "What CIDR would you like to use for your public subnets?"
	envInitPublicCIDRPromptHelp  = "CIDRs used for your public subnets. For example: 10.1.0.0/24,10.1.1.0/24"
	envInitPrivateCIDRPrompt     = "What CIDR would you like to use for your private subnets?"
	envInitPrivateCIDRPromptHelp = "CIDRs used for your private subnets. For example: 10.1.2.0/24,10.1.3.0/24"

	fmtEnvInitCredsPrompt  = "Which credentials would you like to use to create %s?"
	envInitCredsHelpPrompt = `The credentials are used to create your environment in an AWS account and region.
To learn more:
https://aws.github.io/copilot-cli/docs/credentials/#environment-credentials`
	envInitRegionPrompt        = "Which region?"
	envInitDefaultRegionOption = "us-west-2"

	fmtDNSDelegationStart    = "Sharing DNS permissions for this application to account %s."
	fmtDNSDelegationFailed   = "Failed to grant DNS permissions to account %s.\n\n"
	fmtDNSDelegationComplete = "Shared DNS permissions for this application to account %s.\n\n"
	fmtAddEnvToAppStart      = "Linking account %s and region %s to application %s."
	fmtAddEnvToAppFailed     = "Failed to link account %s and region %s to application %s.\n\n"
	fmtAddEnvToAppComplete   = "Linked account %s and region %s to application %s.\n\n"
)

var (
	envInitAppNamePrompt                  = fmt.Sprintf("In which %s would you like to create the environment?", color.Emphasize("application"))
	envInitDefaultConfigSelectOption      = "Yes, use default."
	envInitAdjustEnvResourcesSelectOption = "Yes, but I'd like configure the default resources (CIDR ranges, AZs)."
	envInitImportEnvResourcesSelectOption = "No, I'd like to import existing resources (VPC, subnets)."
	envInitCustomizedEnvTypes             = []string{envInitDefaultConfigSelectOption, envInitAdjustEnvResourcesSelectOption, envInitImportEnvResourcesSelectOption}
)

type importVPCVars struct {
	ID               string
	PublicSubnetIDs  []string
	PrivateSubnetIDs []string
}

func (v importVPCVars) isSet() bool {
	if v.ID != "" {
		return true
	}
	return len(v.PublicSubnetIDs) > 0 || len(v.PrivateSubnetIDs) > 0
}

type adjustVPCVars struct {
	CIDR               net.IPNet
	AZs                []string
	PublicSubnetCIDRs  []string
	PrivateSubnetCIDRs []string
}

func (v adjustVPCVars) isSet() bool {
	if v.CIDR.String() != emptyIPNet.String() {
		return true
	}
	for _, arr := range [][]string{v.AZs, v.PublicSubnetCIDRs, v.PrivateSubnetCIDRs} {
		if len(arr) != 0 {
			return true
		}
	}
	return false
}

type tempCredsVars struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

func (v tempCredsVars) isSet() bool {
	return v.AccessKeyID != "" && v.SecretAccessKey != ""
}

type initEnvVars struct {
	appName       string
	name          string // Name for the environment.
	profile       string // The named profile to use for credential retrieval. Mutually exclusive with tempCreds.
	isProduction  bool   // True means retain resources even after deletion.
	defaultConfig bool   // True means using default environment configuration.

	importVPC importVPCVars // Existing VPC resources to use instead of creating new ones.
	adjustVPC adjustVPCVars // Configure parameters for VPC resources generated while initializing an environment.

	tempCreds tempCredsVars // Temporary credentials to initialize the environment. Mutually exclusive with the profile.
	region    string        // The region to create the environment in.
}

type initEnvOpts struct {
	initEnvVars

	// Interfaces to interact with dependencies.
	sessProvider sessionProvider
	store        store
	envDeployer  deployer
	appDeployer  deployer
	identity     identityService
	envIdentity  identityService
	ec2Client    ec2Client
	iam          roleManager
	cfn          stackExistChecker
	prog         progress
	prompt       prompter
	selVPC       ec2Selector
	selCreds     credsSelector
	selApp       appSelector
	appCFN       appResourcesGetter
	newS3        func(string) (uploader, error)
	uploader     customResourcesUploader

	sess *session.Session // Session pointing to environment's AWS account and region.
}

func newInitEnvOpts(vars initEnvVars) (*initEnvOpts, error) {
	store, err := config.NewStore()
	if err != nil {
		return nil, err
	}
	sessProvider := sessions.NewProvider()
	defaultSession, err := sessProvider.Default()
	if err != nil {
		return nil, err
	}
	cfg, err := profile.NewConfig()
	if err != nil {
		return nil, fmt.Errorf("read named profiles: %w", err)
	}

	prompter := prompt.New()
	return &initEnvOpts{
		initEnvVars:  vars,
		sessProvider: sessProvider,
		store:        store,
		appDeployer:  deploycfn.New(defaultSession),
		identity:     identity.New(defaultSession),
		prog:         termprogress.NewSpinner(log.DiagnosticWriter),
		prompt:       prompter,
		selCreds: &selector.CredsSelect{
			Session: sessProvider,
			Profile: cfg,
			Prompt:  prompter,
		},
		selApp:   selector.NewSelect(prompt.New(), store),
		uploader: template.New(),
		appCFN:   deploycfn.New(defaultSession),
		newS3: func(region string) (uploader, error) {
			sess, err := sessProvider.DefaultWithRegion(region)
			if err != nil {
				return nil, err
			}
			return s3.New(sess), nil
		},
	}, nil
}

// Validate returns an error if the values passed by flags are invalid.
func (o *initEnvOpts) Validate() error {
	if o.name != "" {
		if err := validateEnvironmentName(o.name); err != nil {
			return err
		}
		if err := o.validateDuplicateEnv(); err != nil {
			return err
		}
	}

	if err := o.validateCustomizedResources(); err != nil {
		return err
	}
	return o.validateCredentials()
}

// Ask asks for fields that are required but not passed in.
func (o *initEnvOpts) Ask() error {
	if err := o.askAppName(); err != nil {
		return err
	}
	if err := o.askEnvName(); err != nil {
		return err
	}
	if err := o.askEnvSession(); err != nil {
		return err
	}
	if err := o.askEnvRegion(); err != nil {
		return err
	}
	return o.askCustomizedResources()
}

// Execute deploys a new environment with CloudFormation and adds it to SSM.
func (o *initEnvOpts) Execute() error {
	o.initRuntimeClients()
	app, err := o.store.GetApplication(o.appName)
	if err != nil {
		// Ensure the app actually exists before we do a deployment.
		return err
	}

	envCaller, err := o.envIdentity.Get()
	if err != nil {
		return fmt.Errorf("get identity: %w", err)
	}

	if app.RequiresDNSDelegation() {
		if err := o.delegateDNSFromApp(app, envCaller.Account); err != nil {
			return fmt.Errorf("granting DNS permissions: %w", err)
		}
	}

	// 1. Attempt to create the service linked role if it doesn't exist.
	// If the call fails because the role already exists, nothing to do.
	// If the call fails because the user doesn't have permissions, then the role must be created outside of Copilot.
	_ = o.iam.CreateECSServiceLinkedRole()

	// 2. Add the stack set instance to the app stackset.
	if err := o.addToStackset(&deploycfn.AddEnvToAppOpts{
		App:          app,
		EnvName:      o.name,
		EnvRegion:    aws.StringValue(o.sess.Config.Region),
		EnvAccountID: envCaller.Account,
	}); err != nil {
		return err
	}

	// 3. Upload environment custom resource scripts to the S3 bucket, because of the 4096 characters limit (see
	// https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-properties-lambda-function-code.html#cfn-lambda-function-code-zipfile)
	envRegion := aws.StringValue(o.sess.Config.Region)
	resources, err := o.appCFN.GetAppResourcesByRegion(app, envRegion)
	if err != nil {
		return fmt.Errorf("get app resources: %w", err)
	}
	s3Client, err := o.newS3(envRegion)
	if err != nil {
		return err
	}
	urls, err := o.uploader.UploadEnvironmentCustomResources(s3.CompressAndUploadFunc(func(key string, objects ...s3.NamedBinary) (string, error) {
		return s3Client.ZipAndUpload(resources.S3Bucket, key, objects...)
	}))
	if err != nil {
		return fmt.Errorf("upload custom resources to bucket %s: %w", resources.S3Bucket, err)
	}

	// 4. Start creating the CloudFormation stack for the environment.
	if err := o.deployEnv(app, urls); err != nil {
		return err
	}

	// 5. Get the environment
	env, err := o.envDeployer.GetEnvironment(o.appName, o.name)
	if err != nil {
		return fmt.Errorf("get environment struct for %s: %w", o.name, err)
	}
	env.Prod = o.isProduction
	env.CustomConfig = config.NewCustomizeEnv(o.importVPCConfig(), o.adjustVPCConfig())

	// 6. Store the environment in SSM.
	if err := o.store.CreateEnvironment(env); err != nil {
		return fmt.Errorf("store environment: %w", err)
	}
	log.Successf("Created environment %s in region %s under application %s.\n",
		color.HighlightUserInput(env.Name), color.Emphasize(env.Region), color.HighlightUserInput(env.App))
	return nil
}

// RecommendActions returns follow-up actions the user can take after successfully executing the command.
func (o *initEnvOpts) RecommendActions() error {
	return nil
}

func (o *initEnvOpts) initRuntimeClients() {
	// Initialize environment clients if not set.
	if o.envIdentity == nil {
		o.envIdentity = identity.New(o.sess)
	}
	if o.envDeployer == nil {
		o.envDeployer = deploycfn.New(o.sess)
	}
	if o.cfn == nil {
		o.cfn = cloudformation.New(o.sess)
	}
	if o.iam == nil {
		o.iam = iam.New(o.sess)
	}
}

func (o *initEnvOpts) validateCustomizedResources() error {
	if o.importVPC.isSet() && o.adjustVPC.isSet() {
		return errors.New("cannot specify both import vpc flags and configure vpc flags")
	}
	if (o.importVPC.isSet() || o.adjustVPC.isSet()) && o.defaultConfig {
		return fmt.Errorf("cannot import or configure vpc if --%s is set", defaultConfigFlag)
	}
	if o.importVPC.isSet() {
		// Allow passing in VPC without subnets, but error out early for too few subnets-- we won't prompt the user to select more of one type if they pass in any.
		if len(o.importVPC.PublicSubnetIDs) == 1 {
			return errors.New("at least two public subnets must be imported to enable Load Balancing")
		}
		if len(o.importVPC.PrivateSubnetIDs) == 1 {
			return fmt.Errorf("at least two private subnets must be imported")
		}
	}
	if o.adjustVPC.isSet() {
		if len(o.adjustVPC.AZs) == 1 {
			return errors.New("at least two availability zones must be provided to enable Load Balancing")
		}
	}
	return nil
}

func (o *initEnvOpts) askAppName() error {
	if o.appName != "" {
		return nil
	}

	app, err := o.selApp.Application(envInitAppNamePrompt, envInitAppNameHelpPrompt)
	if err != nil {
		return fmt.Errorf("ask for application: %w", err)
	}
	o.appName = app
	return nil
}

func (o *initEnvOpts) askEnvName() error {
	if o.name != "" {
		return nil
	}

	envName, err := o.prompt.Get(envInitNamePrompt, envInitNameHelpPrompt, validateEnvironmentName, prompt.WithFinalMessage("Environment name:"))
	if err != nil {
		return fmt.Errorf("get environment name: %w", err)
	}
	o.name = envName
	return o.validateDuplicateEnv()
}

func (o *initEnvOpts) askEnvSession() error {
	if o.profile != "" {
		sess, err := o.sessProvider.FromProfile(o.profile)
		if err != nil {
			return fmt.Errorf("create session from profile %s: %w", o.profile, err)
		}
		o.sess = sess
		return nil
	}
	if o.tempCreds.isSet() {
		sess, err := o.sessProvider.FromStaticCreds(o.tempCreds.AccessKeyID, o.tempCreds.SecretAccessKey, o.tempCreds.SessionToken)
		if err != nil {
			return err
		}
		o.sess = sess
		return nil
	}
	sess, err := o.selCreds.Creds(fmt.Sprintf(fmtEnvInitCredsPrompt, color.HighlightUserInput(o.name)), envInitCredsHelpPrompt)
	if err != nil {
		return fmt.Errorf("select creds: %w", err)
	}
	o.sess = sess
	return nil
}

func (o *initEnvOpts) askEnvRegion() error {
	region := aws.StringValue(o.sess.Config.Region)
	if o.region != "" {
		region = o.region
	}
	if region == "" {
		v, err := o.prompt.Get(envInitRegionPrompt, "", nil, prompt.WithDefaultInput(envInitDefaultRegionOption), prompt.WithFinalMessage("Region:"))
		if err != nil {
			return fmt.Errorf("get environment region: %w", err)
		}
		region = v
	}
	o.sess.Config.Region = aws.String(region)
	return nil
}

func (o *initEnvOpts) askCustomizedResources() error {
	if o.defaultConfig {
		return nil
	}
	if o.importVPC.isSet() {
		return o.askImportResources()
	}
	if o.adjustVPC.isSet() {
		return o.askAdjustResources()
	}
	adjustOrImport, err := o.prompt.SelectOne(
		envInitDefaultEnvConfirmPrompt, "",
		envInitCustomizedEnvTypes,
		prompt.WithFinalMessage("Default environment configuration?"))
	if err != nil {
		return fmt.Errorf("select adjusting or importing resources: %w", err)
	}
	switch adjustOrImport {
	case envInitImportEnvResourcesSelectOption:
		return o.askImportResources()
	case envInitAdjustEnvResourcesSelectOption:
		return o.askAdjustResources()
	case envInitDefaultConfigSelectOption:
		return nil
	}
	return nil
}

func (o *initEnvOpts) askImportResources() error {
	if o.selVPC == nil {
		o.selVPC = selector.NewEC2Select(o.prompt, ec2.New(o.sess))
	}
	if o.importVPC.ID == "" {
		vpcID, err := o.selVPC.VPC(envInitVPCSelectPrompt, "")
		if err != nil {
			if err == selector.ErrVPCNotFound {
				log.Errorf(`No existing VPCs were found. You can either:
- Create a new VPC first and then import it.
- Use the default Copilot environment configuration.
`)
			}
			return fmt.Errorf("select VPC: %w", err)
		}
		o.importVPC.ID = vpcID
	}
	if o.ec2Client == nil {
		o.ec2Client = ec2.New(o.sess)
	}
	dnsSupport, err := o.ec2Client.HasDNSSupport(o.importVPC.ID)
	if err != nil {
		return fmt.Errorf("check if VPC %s has DNS support enabled: %w", o.importVPC.ID, err)
	}
	if !dnsSupport {
		log.Errorln(`Looks like you're creating an environment using a VPC with DNS support *disabled*.
Copilot cannot create services or jobs in VPCs without DNS support. We recommend enabling this property.
To learn more about the issue:
https://aws.amazon.com/premiumsupport/knowledge-center/ecs-pull-container-api-error-ecr/`)
		return fmt.Errorf("VPC %s has no DNS support enabled", o.importVPC.ID)
	}
	if o.importVPC.PublicSubnetIDs == nil {
		publicSubnets, err := o.selVPC.Subnets(selector.SubnetsInput{
			Msg:      envInitPublicSubnetsSelectPrompt,
			Help:     "",
			VPCID:    o.importVPC.ID,
			IsPublic: true,
		})
		if err != nil {
			if errors.Is(err, selector.ErrSubnetsNotFound) {
				log.Warningf(`No existing subnets were found in VPC %s.
If you proceed without at least two public subnets, you will not be able to deploy Load Balanced Web Services in this environment.
`, o.importVPC.ID)
			} else {
				return fmt.Errorf("select public subnets: %w", err)
			}
		}
		if len(publicSubnets) == 1 {
			return errors.New("select public subnets: at least two public subnets must be selected to enable Load Balancing")
		}
		o.importVPC.PublicSubnetIDs = publicSubnets
	}
	if o.importVPC.PrivateSubnetIDs == nil {
		privateSubnets, err := o.selVPC.Subnets(selector.SubnetsInput{
			Msg:      envInitPrivateSubnetsSelectPrompt,
			Help:     "",
			VPCID:    o.importVPC.ID,
			IsPublic: false,
		})
		if err != nil {
			if err == selector.ErrSubnetsNotFound {
				log.Errorf(`No existing subnets were found in VPC %s. You can either:
- Create new private subnets and then import them.
- Use the default Copilot environment configuration.`, o.importVPC.ID)
			}
			return fmt.Errorf("select private subnets: %w", err)
		}
		if len(privateSubnets) < 2 {
			return errors.New("select private subnets: at least two private subnets must be selected")
		}
		o.importVPC.PrivateSubnetIDs = privateSubnets
	}
	return nil
}

func (o *initEnvOpts) askAdjustResources() error {
	if o.adjustVPC.CIDR.String() == emptyIPNet.String() {
		vpcCIDRString, err := o.prompt.Get(envInitVPCCIDRPrompt, envInitVPCCIDRPromptHelp, validateCIDR,
			prompt.WithDefaultInput(stack.DefaultVPCCIDR), prompt.WithFinalMessage("VPC CIDR:"))
		if err != nil {
			return fmt.Errorf("get VPC CIDR: %w", err)
		}
		_, vpcCIDR, err := net.ParseCIDR(vpcCIDRString)
		if err != nil {
			return fmt.Errorf("parse VPC CIDR: %w", err)
		}
		o.adjustVPC.CIDR = *vpcCIDR
	}
	azs, err := o.askAZs()
	if err != nil {
		return err
	}
	o.adjustVPC.AZs = azs
	if o.adjustVPC.PublicSubnetCIDRs == nil {
		publicCIDR, err := o.prompt.Get(
			envInitPublicCIDRPrompt, envInitPublicCIDRPromptHelp,
			validatePublicSubnetsCIDR(len(o.adjustVPC.AZs)),
			prompt.WithDefaultInput(stack.DefaultPublicSubnetCIDRs), prompt.WithFinalMessage("Public subnets CIDR:"))
		if err != nil {
			return fmt.Errorf("get public subnet CIDRs: %w", err)
		}
		o.adjustVPC.PublicSubnetCIDRs = strings.Split(publicCIDR, ",")
	}
	if o.adjustVPC.PrivateSubnetCIDRs == nil {
		privateCIDR, err := o.prompt.Get(
			envInitPrivateCIDRPrompt, envInitPrivateCIDRPromptHelp,
			validatePrivateSubnetsCIDR(len(o.adjustVPC.AZs)),
			prompt.WithDefaultInput(stack.DefaultPrivateSubnetCIDRs), prompt.WithFinalMessage("Private subnets CIDR:"))
		if err != nil {
			return fmt.Errorf("get private subnet CIDRs: %w", err)
		}
		o.adjustVPC.PrivateSubnetCIDRs = strings.Split(privateCIDR, ",")
	}
	return nil
}

func (o *initEnvOpts) askAZs() ([]string, error) {
	if o.adjustVPC.AZs != nil {
		return o.adjustVPC.AZs, nil
	}
	if o.ec2Client == nil {
		o.ec2Client = ec2.New(o.sess)
	}
	azs, err := o.ec2Client.ListAZs()
	if err != nil {
		return nil, fmt.Errorf("list availability zones for region %s: %v", aws.StringValue(o.sess.Config.Region), err)
	}

	var options []string
	for _, az := range azs {
		options = append(options, az.Name)
	}
	const minAZs = 2
	if len(options) < minAZs {
		return nil, fmt.Errorf("requires at least %d availability zones (%s) in region %s", minAZs, strings.Join(options, ", "), aws.StringValue(o.sess.Config.Region))
	}
	defaultOptions := make([]string, minAZs)
	for i := 0; i < minAZs; i += 1 {
		defaultOptions[i] = azs[i].Name
	}
	selected, err := o.prompt.MultiSelect(
		envInitAdjustAZPrompt, envInitAdjustAZPromptHelp, options,
		prompt.RequireMinItems(minAZs),
		prompt.WithDefaultSelections(defaultOptions), prompt.WithFinalMessage("AZs:"))
	if err != nil {
		return nil, fmt.Errorf("select availability zones: %v", err)
	}
	return selected, nil
}

func (o *initEnvOpts) validateDuplicateEnv() error {
	_, err := o.store.GetEnvironment(o.appName, o.name)
	if err == nil {
		log.Errorf(`It seems like you are trying to init an environment that already exists.
To recreate the environment, please run:
1. %s
2. And then %s
`,
			color.HighlightCode(fmt.Sprintf("copilot env delete --name %s", o.name)),
			color.HighlightCode(fmt.Sprintf("copilot env init --name %s", o.name)))
		return fmt.Errorf("environment %s already exists", color.HighlightUserInput(o.name))
	}

	var errNoSuchEnvironment *config.ErrNoSuchEnvironment
	if !errors.As(err, &errNoSuchEnvironment) {
		return fmt.Errorf("validate if environment exists: %w", err)
	}
	return nil
}

func (o *initEnvOpts) importVPCConfig() *config.ImportVPC {
	if o.defaultConfig || !o.importVPC.isSet() {
		return nil
	}
	return &config.ImportVPC{
		ID:               o.importVPC.ID,
		PrivateSubnetIDs: o.importVPC.PrivateSubnetIDs,
		PublicSubnetIDs:  o.importVPC.PublicSubnetIDs,
	}
}

func (o *initEnvOpts) adjustVPCConfig() *config.AdjustVPC {
	if o.defaultConfig || !o.adjustVPC.isSet() {
		return nil
	}
	return &config.AdjustVPC{
		CIDR:               o.adjustVPC.CIDR.String(),
		AZs:                o.adjustVPC.AZs,
		PrivateSubnetCIDRs: o.adjustVPC.PrivateSubnetCIDRs,
		PublicSubnetCIDRs:  o.adjustVPC.PublicSubnetCIDRs,
	}
}

func (o *initEnvOpts) deployEnv(app *config.Application, customResourcesURLs map[string]string) error {
	caller, err := o.identity.Get()
	if err != nil {
		return fmt.Errorf("get identity: %w", err)
	}
	deployEnvInput := &deploy.CreateEnvironmentInput{
		Name: o.name,
		App: deploy.AppInformation{
			Name:                o.appName,
			DNSName:             app.Domain,
			AccountPrincipalARN: caller.RootUserARN,
		},
		Prod:                o.isProduction,
		AdditionalTags:      app.Tags,
		CustomResourcesURLs: customResourcesURLs,
		AdjustVPCConfig:     o.adjustVPCConfig(),
		ImportVPCConfig:     o.importVPCConfig(),
		Version:             deploy.LatestEnvTemplateVersion,
	}

	if err := o.cleanUpDanglingRoles(o.appName, o.name); err != nil {
		return err
	}
	if err := o.envDeployer.DeployAndRenderEnvironment(os.Stderr, deployEnvInput); err != nil {
		var existsErr *cloudformation.ErrStackAlreadyExists
		if errors.As(err, &existsErr) {
			// Do nothing if the stack already exists.
			return nil
		}
		// The stack failed to create due to an unexpect reason.
		// Delete the retained roles created part of the stack.
		o.tryDeletingEnvRoles(o.appName, o.name)
		return err
	}
	return nil
}

func (o *initEnvOpts) addToStackset(opts *deploycfn.AddEnvToAppOpts) error {
	o.prog.Start(fmt.Sprintf(fmtAddEnvToAppStart, color.Emphasize(opts.EnvAccountID), color.Emphasize(opts.EnvRegion), color.HighlightUserInput(o.appName)))
	if err := o.appDeployer.AddEnvToApp(opts); err != nil {
		o.prog.Stop(log.Serrorf(fmtAddEnvToAppFailed, color.Emphasize(opts.EnvAccountID), color.Emphasize(opts.EnvRegion), color.HighlightUserInput(o.appName)))
		return fmt.Errorf("deploy env %s to application %s: %w", opts.EnvName, opts.App.Name, err)
	}
	o.prog.Stop(log.Ssuccessf(fmtAddEnvToAppComplete, color.Emphasize(opts.EnvAccountID), color.Emphasize(opts.EnvRegion), color.HighlightUserInput(o.appName)))

	return nil
}

func (o *initEnvOpts) delegateDNSFromApp(app *config.Application, accountID string) error {
	// By default, our DNS Delegation permits same account delegation.
	if accountID == app.AccountID {
		return nil
	}

	o.prog.Start(fmt.Sprintf(fmtDNSDelegationStart, color.HighlightUserInput(accountID)))
	if err := o.appDeployer.DelegateDNSPermissions(app, accountID); err != nil {
		o.prog.Stop(log.Serrorf(fmtDNSDelegationFailed, color.HighlightUserInput(accountID)))
		return err
	}
	o.prog.Stop(log.Ssuccessf(fmtDNSDelegationComplete, color.HighlightUserInput(accountID)))
	return nil
}

func (o *initEnvOpts) validateCredentials() error {
	if o.profile != "" && o.tempCreds.AccessKeyID != "" {
		return fmt.Errorf("cannot specify both --%s and --%s", profileFlag, accessKeyIDFlag)
	}
	if o.profile != "" && o.tempCreds.SecretAccessKey != "" {
		return fmt.Errorf("cannot specify both --%s and --%s", profileFlag, secretAccessKeyFlag)
	}
	if o.profile != "" && o.tempCreds.SessionToken != "" {
		return fmt.Errorf("cannot specify both --%s and --%s", profileFlag, sessionTokenFlag)
	}
	return nil
}

// cleanUpDanglingRoles deletes any IAM roles created for the same app and env that were left over from a previous
// environment creation.
func (o *initEnvOpts) cleanUpDanglingRoles(app, env string) error {
	exists, err := o.cfn.Exists(stack.NameForEnv(app, env))
	if err != nil {
		return fmt.Errorf("check if stack %s exists: %w", stack.NameForEnv(app, env), err)
	}
	if exists {
		return nil
	}
	// There is no environment stack. Either the customer ran "env delete" before, or it's their
	// first time running this command.
	// We should clean up any IAM roles that were *not* deleted during "env delete"
	// before re-creating the stack otherwise the deployment will fail.
	o.tryDeletingEnvRoles(app, env)
	return nil
}

// tryDeletingEnvRoles attempts a best effort deletion of IAM roles created from an environment.
// To ensure that the roles being deleted were created by Copilot, we check if the copilot-environment tag
// is applied to the role.
func (o *initEnvOpts) tryDeletingEnvRoles(app, env string) {
	roleNames := []string{
		fmt.Sprintf("%s-CFNExecutionRole", stack.NameForEnv(app, env)),
		fmt.Sprintf("%s-EnvManagerRole", stack.NameForEnv(app, env)),
	}
	for _, roleName := range roleNames {
		tags, err := o.iam.ListRoleTags(roleName)
		if err != nil {
			continue
		}
		if _, hasTag := tags[deploy.EnvTagKey]; !hasTag {
			continue
		}
		_ = o.iam.DeleteRole(roleName)
	}
}

// buildEnvInitCmd builds the command for adding an environment.
func buildEnvInitCmd() *cobra.Command {
	vars := initEnvVars{}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Creates a new environment in your application.",
		Example: `
  Creates a test environment in your "default" AWS profile using default configuration.
  /code $ copilot env init --name test --profile default --default-config

  Creates a prod-iad environment using your "prod-admin" AWS profile.
  /code $ copilot env init --name prod-iad --profile prod-admin --prod

  Creates an environment with imported VPC resources.
  /code $ copilot env init --import-vpc-id vpc-099c32d2b98cdcf47 \
  /code --import-public-subnets subnet-013e8b691862966cf,subnet-014661ebb7ab8681a \
  /code --import-private-subnets subnet-055fafef48fb3c547,subnet-00c9e76f288363e7f

  Creates an environment with overridden CIDRs and AZs.
  /code $ copilot env init --override-vpc-cidr 10.1.0.0/16 \
  /code --override-az-names us-west-2b,us-west-2c \
  /code --override-public-cidrs 10.1.0.0/24,10.1.1.0/24 \
  /code --override-private-cidrs 10.1.2.0/24,10.1.3.0/24`,
		RunE: runCmdE(func(cmd *cobra.Command, args []string) error {
			opts, err := newInitEnvOpts(vars)
			if err != nil {
				return err
			}
			return run(opts)
		}),
	}
	cmd.Flags().StringVarP(&vars.appName, appFlag, appFlagShort, tryReadingAppName(), appFlagDescription)
	cmd.Flags().StringVarP(&vars.name, nameFlag, nameFlagShort, "", envFlagDescription)
	cmd.Flags().StringVar(&vars.profile, profileFlag, "", profileFlagDescription)
	cmd.Flags().StringVar(&vars.tempCreds.AccessKeyID, accessKeyIDFlag, "", accessKeyIDFlagDescription)
	cmd.Flags().StringVar(&vars.tempCreds.SecretAccessKey, secretAccessKeyFlag, "", secretAccessKeyFlagDescription)
	cmd.Flags().StringVar(&vars.tempCreds.SessionToken, sessionTokenFlag, "", sessionTokenFlagDescription)
	cmd.Flags().StringVar(&vars.region, regionFlag, "", envRegionTokenFlagDescription)

	cmd.Flags().BoolVar(&vars.isProduction, prodEnvFlag, false, prodEnvFlagDescription)

	cmd.Flags().StringVar(&vars.importVPC.ID, vpcIDFlag, "", vpcIDFlagDescription)
	cmd.Flags().StringSliceVar(&vars.importVPC.PublicSubnetIDs, publicSubnetsFlag, nil, publicSubnetsFlagDescription)
	cmd.Flags().StringSliceVar(&vars.importVPC.PrivateSubnetIDs, privateSubnetsFlag, nil, privateSubnetsFlagDescription)

	cmd.Flags().IPNetVar(&vars.adjustVPC.CIDR, overrideVPCCIDRFlag, net.IPNet{}, overrideVPCCIDRFlagDescription)
	cmd.Flags().StringSliceVar(&vars.adjustVPC.AZs, overrideAZsFlag, nil, overrideAZsFlagDescription)
	// TODO: use IPNetSliceVar when it is available (https://github.com/spf13/pflag/issues/273).
	cmd.Flags().StringSliceVar(&vars.adjustVPC.PublicSubnetCIDRs, overridePublicSubnetCIDRsFlag, nil, overridePublicSubnetCIDRsFlagDescription)
	cmd.Flags().StringSliceVar(&vars.adjustVPC.PrivateSubnetCIDRs, overridePrivateSubnetCIDRsFlag, nil, overridePrivateSubnetCIDRsFlagDescription)
	cmd.Flags().BoolVar(&vars.defaultConfig, defaultConfigFlag, false, defaultConfigFlagDescription)

	flags := pflag.NewFlagSet("Common", pflag.ContinueOnError)
	flags.AddFlag(cmd.Flags().Lookup(appFlag))
	flags.AddFlag(cmd.Flags().Lookup(nameFlag))
	flags.AddFlag(cmd.Flags().Lookup(profileFlag))
	flags.AddFlag(cmd.Flags().Lookup(accessKeyIDFlag))
	flags.AddFlag(cmd.Flags().Lookup(secretAccessKeyFlag))
	flags.AddFlag(cmd.Flags().Lookup(sessionTokenFlag))
	flags.AddFlag(cmd.Flags().Lookup(regionFlag))
	flags.AddFlag(cmd.Flags().Lookup(defaultConfigFlag))
	flags.AddFlag(cmd.Flags().Lookup(prodEnvFlag))

	resourcesImportFlag := pflag.NewFlagSet("Import Existing Resources", pflag.ContinueOnError)
	resourcesImportFlag.AddFlag(cmd.Flags().Lookup(vpcIDFlag))
	resourcesImportFlag.AddFlag(cmd.Flags().Lookup(publicSubnetsFlag))
	resourcesImportFlag.AddFlag(cmd.Flags().Lookup(privateSubnetsFlag))

	resourcesConfigFlag := pflag.NewFlagSet("Configure Default Resources", pflag.ContinueOnError)
	resourcesConfigFlag.AddFlag(cmd.Flags().Lookup(overrideVPCCIDRFlag))
	resourcesConfigFlag.AddFlag(cmd.Flags().Lookup(overrideAZsFlag))
	resourcesConfigFlag.AddFlag(cmd.Flags().Lookup(overridePublicSubnetCIDRsFlag))
	resourcesConfigFlag.AddFlag(cmd.Flags().Lookup(overridePrivateSubnetCIDRsFlag))

	cmd.Annotations = map[string]string{
		// The order of the sections we want to display.
		"sections":                    "Common,Import Existing Resources,Configure Default Resources",
		"Common":                      flags.FlagUsages(),
		"Import Existing Resources":   resourcesImportFlag.FlagUsages(),
		"Configure Default Resources": resourcesConfigFlag.FlagUsages(),
	}

	cmd.SetUsageTemplate(`{{h1 "Usage"}}{{if .Runnable}}
  {{.UseLine}}{{end}}{{$annotations := .Annotations}}{{$sections := split .Annotations.sections ","}}{{if gt (len $sections) 0}}

{{range $i, $sectionName := $sections}}{{h1 (print $sectionName " Flags")}}
{{(index $annotations $sectionName) | trimTrailingWhitespaces}}{{if ne (inc $i) (len $sections)}}

{{end}}{{end}}{{end}}{{if .HasAvailableInheritedFlags}}

{{h1 "Global Flags"}}
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasExample}}

{{h1 "Examples"}}{{code .Example}}{{end}}
`)

	return cmd
}
