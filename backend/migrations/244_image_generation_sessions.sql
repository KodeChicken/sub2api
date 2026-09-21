CREATE TABLE image_generation_sessions (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title VARCHAR(80) NOT NULL,
    sort_order BIGINT NOT NULL,
    draft JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_image_generation_sessions_user_order ON image_generation_sessions (user_id, sort_order DESC);

CREATE TABLE image_generation_records (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES image_generation_sessions(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_image_generation_records_user_created ON image_generation_records (user_id, created_at DESC);
CREATE INDEX idx_image_generation_records_session_created ON image_generation_records (session_id, created_at DESC);
CREATE INDEX idx_image_generation_records_task ON image_generation_records ((payload->>'taskId')) WHERE payload->>'taskId' IS NOT NULL;

CREATE TABLE image_generation_assets (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES image_generation_sessions(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    mime_type VARCHAR(32) NOT NULL,
    byte_size BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_image_generation_assets_session ON image_generation_assets (session_id);
