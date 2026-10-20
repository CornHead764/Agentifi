-- An envelope carried an overspend into the next month whenever it carried
-- unspent funds, and neither when auto-release was on. Rolling under (carrying
-- an overspend) is now its own setting. An envelope that did not auto-release
-- carried both directions, so it rolls under; one that did carried neither, so
-- it does not. auto_release_rollover keeps its meaning: unspent funds are
-- released rather than carried.

-- +goose Up
ALTER TABLE envelopes ADD COLUMN roll_under boolean DEFAULT true NOT NULL;

UPDATE envelopes SET roll_under = NOT auto_release_rollover;

-- +goose Down
-- An envelope that rolled under but auto-released carried only its overspend;
-- the earlier schema has no such setting, so it carries nothing there.
ALTER TABLE envelopes DROP COLUMN roll_under;
