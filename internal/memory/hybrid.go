package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/tamld/g8s/internal/vault"
)

// HybridRetrieve concurrently executes multi-modal retrieval across semantic,
// episodic, and vector cognitive memory dimensions, returning a consolidated result.
func (a *LocalSQLiteMemoryAdapter) HybridRetrieve(ctx context.Context, req HybridQuery) (*HybridResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := &HybridResult{}
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		errList []error
	)

	// 1. Semantic Search (FTS5 BM25)
	if req.Query != "" && a.vault != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lessons, err := a.SearchKnowledge(ctx, req.Query, req.Limit)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errList = append(errList, fmt.Errorf("hybrid: semantic search: %w", err))
				return
			}
			result.SemanticLessons = lessons
		}()
	}

	// 2. Episodic Lineage (Causal DAG Tree)
	if req.TaskID != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tree, err := a.GetLineageTree(ctx, req.TaskID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errList = append(errList, fmt.Errorf("hybrid: lineage tree: %w", err))
				return
			}
			result.LineageTree = tree
		}()
	}

	// 3. Vector Similarity (Pure-Go Cosine Distance)
	if len(req.Vector) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			matches, err := a.SearchVector(ctx, req.Vector, req.Limit, req.MinScore)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errList = append(errList, fmt.Errorf("hybrid: vector search: %w", err))
				return
			}
			result.VectorMatches = matches
		}()
	}

	wg.Wait()

	if len(errList) > 0 {
		return nil, errList[0]
	}

	if result.SemanticLessons == nil {
		result.SemanticLessons = []vault.SearchResult{}
	}
	if result.VectorMatches == nil {
		result.VectorMatches = []VectorMatch{}
	}

	return result, nil
}
