CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE SCHEMA IF NOT EXISTS velis;

COMMENT ON SCHEMA velis IS 'Velis persistent business and derived data';
