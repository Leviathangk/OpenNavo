-- +goose Up
ALTER TABLE link_apps DROP CONSTRAINT link_apps_pricing_check,
 ADD CONSTRAINT link_apps_pricing_check CHECK(pricing IN('free','paid','freemium','subscription','open_source','unknown')) NOT VALID;
-- +goose Down
-- Refuse to invent a known price when rolling back unknown values.
ALTER TABLE link_apps DROP CONSTRAINT link_apps_pricing_check,
 ADD CONSTRAINT link_apps_pricing_check CHECK(pricing IN('free','paid','freemium','subscription','open_source'));
