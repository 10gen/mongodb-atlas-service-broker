package broker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mongodb/mongodb-atlas-service-broker/pkg/atlas"
	"github.com/pivotal-cf/brokerapi"
)

// idPrefix will be prepended to service and plan IDs to ensure their uniqueness.
const idPrefix = "aosb-cluster"

// providerNames contains all the available cloud providers on which clusters
// may be provisioned. The available instance sizes for each provider are
// fetched dynamically from the Atlas API.
var providerNames = []string{"AWS", "GCP", "AZURE"}

// Services generates the service catalog which will be presented to consumers of the API.
func (b Broker) Services(ctx context.Context) ([]brokerapi.Service, error) {
	b.logger.Info("Retrieving service catalog")

	services := make([]brokerapi.Service, len(providerNames))

	for i, providerName := range providerNames {
		provider, err := b.atlas.GetProvider(providerName)
		if err != nil {
			return services, err
		}

		// Create a CLI-friendly and user-friendly name. Will be displayed in the
		// marketplace generated by the service catalog.
		catalogName := fmt.Sprintf("mongodb-atlas-%s", strings.ToLower(provider.Name))

		services[i] = brokerapi.Service{
			ID:                   serviceIDForProvider(provider),
			Name:                 catalogName,
			Description:          fmt.Sprintf(`Atlas cluster hosted on "%s"`, provider.Name),
			Bindable:             true,
			InstancesRetrievable: false,
			BindingsRetrievable:  false,
			Metadata:             nil,
			PlanUpdatable:        true,
			Plans:                plansForProvider(provider),
		}
	}

	return services, nil
}

// findProviderAndInstanceSizeByIDs will search all available providers and
// instance sizes to find the ones matching the specified service and plan ID.
func (b Broker) findProviderAndInstanceSizeByIDs(serviceID, planID string) (*atlas.Provider, *atlas.InstanceSize, error) {
	for _, providerName := range providerNames {
		provider, err := b.atlas.GetProvider(providerName)
		if err != nil {
			return nil, nil, err
		}

		if serviceIDForProvider(provider) == serviceID {
			for _, instanceSize := range provider.InstanceSizes {
				if planIDForInstanceSize(provider, instanceSize) == planID {
					return provider, &instanceSize, nil
				}
			}
		}
	}

	return nil, nil, errors.New("invalid service ID or plan ID")
}

// plansForProvider will convert the available instance sizes for a provider
// to service plans for the broker.
func plansForProvider(provider *atlas.Provider) []brokerapi.ServicePlan {
	var plans []brokerapi.ServicePlan

	for _, instanceSize := range provider.InstanceSizes {
		plan := brokerapi.ServicePlan{
			ID:          planIDForInstanceSize(provider, instanceSize),
			Name:        instanceSize.Name,
			Description: fmt.Sprintf("Instance size \"%s\"", instanceSize.Name),
		}

		plans = append(plans, plan)
	}

	return plans
}

// serviceIDForProvider will generate a globally unique ID for a provider.
func serviceIDForProvider(provider *atlas.Provider) string {
	return fmt.Sprintf("%s-service-%s", idPrefix, strings.ToLower(provider.Name))
}

// planIDForInstanceSize will generate a globally unique ID for an instance size
// on a specific provider.
func planIDForInstanceSize(provider *atlas.Provider, instanceSize atlas.InstanceSize) string {
	return fmt.Sprintf("%s-plan-%s-%s", idPrefix, strings.ToLower(provider.Name), strings.ToLower(instanceSize.Name))
}
