-- Setup pgvector extension
-- Run this as a superuser on your PostgreSQL database

CREATE EXTENSION IF NOT EXISTS vector;

-- Verify installation
SELECT * FROM pg_extension WHERE extname = 'vector';
