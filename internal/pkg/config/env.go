// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/ssm"
)

// Environment represents a deployment environment in an application.
type Environment struct {
	App              string        `json:"app"`                    // Name of the app this environment belongs to.
	Name             string        `json:"name"`                   // Name of the environment, must be unique within a App.
	Region           string        `json:"region"`                 // Name of the region this environment is stored in.
	AccountID        string        `json:"accountID"`              // Account ID of the account this environment is stored in.
	Prod             bool          `json:"prod"`                   // Whether or not this environment is a production environment.
	RegistryURL      string        `json:"registryURL"`            // URL For ECR Registry for this environment.
	ExecutionRoleARN string        `json:"executionRoleARN"`       // ARN used by CloudFormation to make modification to the environment stack.
	ManagerRoleARN   string        `json:"managerRoleARN"`         // ARN for the manager role assumed to manipulate the environment and its services.
	CustomConfig     *CustomizeEnv `json:"customConfig,omitempty"` // Custom environment configuration by users.
}

// CustomizeEnv represents the custom environment config.
type CustomizeEnv struct {
	ImportVPC *ImportVPC `json:"importVPC,omitempty"`
	VPCConfig *AdjustVPC `json:"adjustVPC,omitempty"`
}

// NewCustomizeEnv returns a new CustomizeEnv struct.
func NewCustomizeEnv(importVPC *ImportVPC, adjustVPC *AdjustVPC) *CustomizeEnv {
	if importVPC == nil && adjustVPC == nil {
		return nil
	}
	return &CustomizeEnv{
		ImportVPC: importVPC,
		VPCConfig: adjustVPC,
	}
}

// ImportVPC holds the fields to import VPC resources.
type ImportVPC struct {
	ID               string   `json:"id"` // ID for the VPC.
	PublicSubnetIDs  []string `json:"publicSubnetIDs"`
	PrivateSubnetIDs []string `json:"privateSubnetIDs"`
}

// AdjustVPC holds the fields to adjust default VPC resources.
type AdjustVPC struct {
	CIDR               string   `json:"cidr"` // CIDR range for the VPC.
	AZs                []string `json:"availabilityZoneNames"`
	PublicSubnetCIDRs  []string `json:"publicSubnetCIDRs"`
	PrivateSubnetCIDRs []string `json:"privateSubnetCIDRs"`
}

// CreateEnvironment instantiates a new environment within an existing App. Skip if
// the environment already exists in the App.
func (s *Store) CreateEnvironment(environment *Environment) error {
	if _, err := s.GetApplication(environment.App); err != nil {
		return err
	}

	environmentPath := fmt.Sprintf(fmtEnvParamPath, environment.App, environment.Name)
	data, err := marshal(environment)
	if err != nil {
		return fmt.Errorf("serializing environment %s: %w", environment.Name, err)
	}

	_, err = s.ssmClient.PutParameter(&ssm.PutParameterInput{
		Name:        aws.String(environmentPath),
		Description: aws.String(fmt.Sprintf("The %s deployment stage", environment.Name)),
		Type:        aws.String(ssm.ParameterTypeString),
		Value:       aws.String(data),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case ssm.ErrCodeParameterAlreadyExists:
				return nil
			}
		}
		return fmt.Errorf("create environment %s in application %s: %w", environment.Name, environment.App, err)
	}
	return nil
}

// GetEnvironment gets an environment belonging to a particular application by name. If no environment is found
// it returns ErrNoSuchEnvironment.
func (s *Store) GetEnvironment(appName string, environmentName string) (*Environment, error) {
	environmentPath := fmt.Sprintf(fmtEnvParamPath, appName, environmentName)
	environmentParam, err := s.ssmClient.GetParameter(&ssm.GetParameterInput{
		Name: aws.String(environmentPath),
	})

	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case ssm.ErrCodeParameterNotFound:
				return nil, &ErrNoSuchEnvironment{
					ApplicationName: appName,
					EnvironmentName: environmentName,
				}
			}
		}
		return nil, fmt.Errorf("get environment %s in application %s: %w", environmentName, appName, err)
	}

	var env Environment
	err = json.Unmarshal([]byte(*environmentParam.Parameter.Value), &env)
	if err != nil {
		return nil, fmt.Errorf("read configuration for environment %s in application %s: %w", environmentName, appName, err)
	}
	return &env, nil
}

// ListEnvironments returns all environments belonging to a particular application.
func (s *Store) ListEnvironments(appName string) ([]*Environment, error) {
	var environments []*Environment

	environmentsPath := fmt.Sprintf(rootEnvParamPath, appName)
	serializedEnvs, err := s.listParams(environmentsPath)
	if err != nil {
		return nil, fmt.Errorf("list environments for application %s: %w", appName, err)
	}
	for _, serializedEnv := range serializedEnvs {
		var env Environment
		if err := json.Unmarshal([]byte(*serializedEnv), &env); err != nil {
			return nil, fmt.Errorf("read environment configuration for application %s: %w", appName, err)
		}

		environments = append(environments, &env)
	}
	// non-prod env before prod env. sort by alphabetically if same
	sort.SliceStable(environments, func(i, j int) bool { return environments[i].Name < environments[j].Name })
	sort.SliceStable(environments, func(i, j int) bool { return !environments[i].Prod })
	return environments, nil
}

// DeleteEnvironment removes an environment from SSM.
// If the environment does not exist in the store or is successfully deleted then returns nil. Otherwise, returns an error.
func (s *Store) DeleteEnvironment(appName, environmentName string) error {
	paramName := fmt.Sprintf(fmtEnvParamPath, appName, environmentName)
	_, err := s.ssmClient.DeleteParameter(&ssm.DeleteParameterInput{
		Name: aws.String(paramName),
	})

	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case ssm.ErrCodeParameterNotFound:
				return nil
			}
		}
		return fmt.Errorf("delete environment %s from application %s: %w", environmentName, appName, err)
	}
	return nil
}
