package management

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// Reads that agents edit from return the revision their writes check (decision D7), so a caller can pass
// it back as X-Expected-Revision. Each read names the write whose revision it reports.
var readRevisionWrites = map[string]string{
	"GetAdminPackage":    "UpdatePackageMeta",
	"GetAdminCollection": "UpdateCollection",
	"GetAdminRelease":    "UpdateRelease",
	"GetFeature":         "UpdateFeature",
	"GetCategoryTree":    "UpdateCategory",
}

// withReadRevision adds "revision" to the result of a read in readRevisionWrites; the category tree gets
// one per node.
func (s *Service) withReadRevision(ctx context.Context, op string, in Input, result any) (any, error) {
	write, ok := readRevisionWrites[op]
	if !ok || result == nil {
		return result, nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if op == "GetCategoryTree" {
		var nodes []map[string]any
		if err := json.Unmarshal(raw, &nodes); err != nil {
			return nil, err
		}
		if err := s.addNodeRevisions(ctx, write, nodes); err != nil {
			return nil, err
		}
		return nodes, nil
	}
	var view map[string]any
	if err := json.Unmarshal(raw, &view); err != nil {
		return nil, err
	}
	if view["revision"], err = s.CurrentRevision(ctx, write, Input{ID: in.ID}); err != nil {
		return nil, err
	}
	return view, nil
}

func (s *Service) addNodeRevisions(ctx context.Context, write string, nodes []map[string]any) error {
	for _, node := range nodes {
		revision, err := s.CurrentRevision(ctx, write, Input{ID: valueID(node["id"])})
		if err != nil {
			return err
		}
		node["revision"] = revision
		children, _ := node["children"].([]any)
		for _, child := range children {
			if item, ok := child.(map[string]any); ok {
				if err := s.addNodeRevisions(ctx, write, []map[string]any{item}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// readWithRevision keeps every content query and revision query on one snapshot, including tree nodes.
func (s *Service) readWithRevision(ctx context.Context, op string, in Input) (any, error) {
	var result any
	err := s.Store.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		copy := *s
		st := *s.Store
		st.DB = tx
		copy.Store = &st
		var err error
		result, err = copy.execute(ctx, op, in)
		if err != nil {
			return err
		}
		result, err = copy.withReadRevision(ctx, op, in, result)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, store.MapAdminError(err)
	}
	return result, nil
}
