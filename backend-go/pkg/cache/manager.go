package cache

import (
	"context"
	"log"
	"sync"
	"time"

	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/plugin"
)

// CacheManager handles background caching of all host data
type CacheManager struct {
	store          *hostcache.Store
	nodeResolver   plugin.NodeResolver
	runnerResolver plugin.RunnerResolver
	cache          *plugin.ProbeCache
	interval       time.Duration
	stop           chan struct{}
	wg             sync.WaitGroup
	mu             sync.Mutex
	lastUpdate     map[string]time.Time
}

// NewCacheManager creates a new cache manager
func NewCacheManager(store *hostcache.Store, nodeResolver plugin.NodeResolver, runnerResolver plugin.RunnerResolver, cache *plugin.ProbeCache) *CacheManager {
	return &CacheManager{
		store:          store,
		nodeResolver:   nodeResolver,
		runnerResolver: runnerResolver,
		cache:          cache,
		interval:       3 * time.Second,
		stop:           make(chan struct{}),
		lastUpdate:     make(map[string]time.Time),
	}
}

// Start begins background caching
func (cm *CacheManager) Start() {
	cm.wg.Add(1)
	go func() {
		defer cm.wg.Done()
		ticker := time.NewTicker(cm.interval)
		defer ticker.Stop()

		// Initial sync
		cm.cacheAllHosts()

		for {
			select {
			case <-cm.stop:
				return
			case <-ticker.C:
				cm.cacheAllHosts()
			}
		}
	}()
}

// Stop stops background caching gracefully
func (cm *CacheManager) Stop() {
	close(cm.stop)
	cm.wg.Wait()
}

func (cm *CacheManager) cacheAllHosts() {
	if cm.store == nil {
		return
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Get all hosts from cache
	hosts, err := cm.store.GetAllHosts()
	if err != nil {
		log.Printf("cache: failed to get hosts: %v", err)
		return
	}

	for _, host := range hosts {
		ctx := context.Background()
		nodeID := host.ID

		// Skip recently updated hosts (less than 2 seconds ago)
		if last, ok := cm.lastUpdate[nodeID]; ok && time.Since(last) < 2*time.Second {
			continue
		}
		cm.lastUpdate[nodeID] = time.Now()

		// Probe and cache plugins/modules
		_ = cm.cachePlugins(ctx, nodeID)
	}
}

func (cm *CacheManager) cachePlugins(ctx context.Context, nodeID string) error {
	// Defensive: a manager constructed without resolvers (e.g. a store-less
	// plugin API) must no-op rather than nil-panic on its tick.
	if cm.nodeResolver == nil || cm.runnerResolver == nil || cm.cache == nil {
		return nil
	}
	facts, ok, err := cm.nodeResolver.ResolveNode(nodeID)
	if err != nil {
		log.Printf("cache: failed to resolve facts for %s: %v", nodeID, err)
		return err
	}
	if !ok {
		log.Printf("cache: host %s not found", nodeID)
		return nil
	}

	runner, err := cm.runnerResolver.ResolveRunner(ctx, facts)
	if err != nil {
		log.Printf("cache: failed to resolve runner for %s: %v", nodeID, err)
		cm.lastUpdate[nodeID] = time.Now().Add(30 * time.Second)
		return err
	}

	all := plugin.List()
	for _, p := range all {
		meta := p.Meta()
		isCompat := p.Compatible(facts)

		if !isCompat {
			continue
		}

		// Respect cache TTLs instead of re-probing fresh entries on every tick
		if _, ok := cm.cache.Get(nodeID, meta.ID); ok {
			continue
		}

		// Probe the plugin with a bounded 5s timeout
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		det, probeErr := p.Probe(probeCtx, runner, facts)
		cancel()
		if probeErr != nil {
			det = plugin.Detection{
				Detected: false,
				Status:   "ERROR",
				Error:    probeErr.Error(),
			}
		}

		// Cache the detection result
		cm.cache.Set(nodeID, meta.ID, meta.Kind, det)

		// A capability found on the host may need controller-side Ansible
		// modules; install them now so the next playbook run just works.
		if det.Detected {
			plugin.EnsureCollectionsForDetection(p, det, facts)
		}
	}

	return nil
}
