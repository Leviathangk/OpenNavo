-- +goose Up
CREATE TABLE indexnow_daily_budget (
 day DATE PRIMARY KEY,
 reserved INTEGER NOT NULL DEFAULT 0 CHECK (reserved >= 0),
 accepted INTEGER NOT NULL DEFAULT 0 CHECK (accepted >= 0)
);

-- +goose Down
DROP TABLE indexnow_daily_budget;
