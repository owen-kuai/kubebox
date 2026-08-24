package persistence

import "context"

// Migrate creates the governance tables for the selected SQL dialect.
// Production should wrap this with the deployment's migration lock.
func (s *SQLStore) Migrate(ctx context.Context) error {
	for _, statement := range s.SchemaStatements() {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
