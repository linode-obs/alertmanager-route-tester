package alertmanager

import (
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"

	amconfig "github.com/prometheus/alertmanager/config"
	"github.com/prometheus/alertmanager/dispatch"
	"github.com/prometheus/alertmanager/featurecontrol"
	"github.com/prometheus/alertmanager/matcher/compat"
	"github.com/prometheus/common/model"
	"gopkg.in/yaml.v3"
)

var nativeMatcherParserMu sync.Mutex

type nativeRouteTree struct {
	root        *dispatch.Route
	localRoutes map[*dispatch.Route]*Route
	paths       map[*dispatch.Route][]*dispatch.Route
}

type nativeRouteConfig struct {
	Route     *Route           `yaml:"route"`
	Receivers []nativeReceiver `yaml:"receivers"`
}

type nativeReceiver struct {
	Name string `yaml:"name"`
}

func newNativeRouteTree(nativeRoot *amconfig.Route, localRoot *Route) (*nativeRouteTree, error) {
	if nativeRoot == nil || localRoot == nil {
		return nil, fmt.Errorf("no route configuration found")
	}

	tree := &nativeRouteTree{
		root:        dispatch.NewRoute(nativeRoot, nil),
		localRoutes: make(map[*dispatch.Route]*Route),
		paths:       make(map[*dispatch.Route][]*dispatch.Route),
	}
	if err := tree.associate(tree.root, localRoot, nil); err != nil {
		return nil, err
	}
	return tree, nil
}

func (tree *nativeRouteTree) associate(nativeRoute *dispatch.Route, localRoute *Route, parentPath []*dispatch.Route) error {
	if len(nativeRoute.Routes) != len(localRoute.Routes) {
		return fmt.Errorf("route tree size differs at index %d", nativeRoute.Idx)
	}

	path := make([]*dispatch.Route, len(parentPath)+1)
	copy(path, parentPath)
	path[len(parentPath)] = nativeRoute
	tree.localRoutes[nativeRoute] = localRoute
	tree.paths[nativeRoute] = path

	for index, child := range nativeRoute.Routes {
		if err := tree.associate(child, localRoute.Routes[index], path); err != nil {
			return err
		}
	}
	return nil
}

func nativeRouteTreeForConfig(config *Config) (*nativeRouteTree, error) {
	if config == nil || config.Route == nil {
		return nil, fmt.Errorf("no route configuration found")
	}
	if config.nativeRoutes != nil {
		return config.nativeRoutes, nil
	}

	names := make(map[string]bool)
	for _, receiver := range config.Receivers {
		if receiver.Name != "" {
			names[receiver.Name] = true
		}
	}
	collectRouteReceiverNames(config.Route, names)

	receivers := make([]nativeReceiver, 0, len(names))
	for name := range names {
		receivers = append(receivers, nativeReceiver{Name: name})
	}
	sort.Slice(receivers, func(i, j int) bool {
		return receivers[i].Name < receivers[j].Name
	})

	encoded, err := yaml.Marshal(nativeRouteConfig{Route: config.Route, Receivers: receivers})
	if err != nil {
		return nil, fmt.Errorf("marshal route configuration: %w", err)
	}
	nativeConfig, err := loadNativeConfig(string(encoded), MatcherModeFallback)
	if err != nil {
		return nil, fmt.Errorf("parse route configuration with Alertmanager: %w", err)
	}
	return newNativeRouteTree(nativeConfig.Route, config.Route)
}

func loadNativeConfig(contents string, mode MatcherMode) (*amconfig.Config, error) {
	feature, err := matcherModeFeature(mode)
	if err != nil {
		return nil, err
	}

	nativeMatcherParserMu.Lock()
	defer nativeMatcherParserMu.Unlock()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	flags, err := featurecontrol.NewFlags(logger, feature)
	if err != nil {
		return nil, err
	}
	compat.InitFromFlags(logger, flags)
	return amconfig.Load(contents)
}

func matcherModeFeature(mode MatcherMode) (string, error) {
	switch mode {
	case "", MatcherModeFallback:
		return "", nil
	case MatcherModeClassic:
		return featurecontrol.FeatureClassicMode, nil
	case MatcherModeUTF8Strict:
		return featurecontrol.FeatureUTF8StrictMode, nil
	default:
		return "", fmt.Errorf("unsupported matcher mode %q", mode)
	}
}

func collectRouteReceiverNames(route *Route, names map[string]bool) {
	if route == nil {
		return
	}
	if route.Receiver != "" {
		names[route.Receiver] = true
	}
	for _, child := range route.Routes {
		collectRouteReceiverNames(child, names)
	}
}

func nativeLabels(labels map[string]string) model.LabelSet {
	set := make(model.LabelSet, len(labels))
	for name, value := range labels {
		set[model.LabelName(name)] = model.LabelValue(value)
	}
	return set
}
