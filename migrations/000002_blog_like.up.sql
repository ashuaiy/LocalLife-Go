CREATE TABLE blog_like (
    blog_id BIGINT UNSIGNED NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (blog_id, user_id),
    KEY idx_blog_like_user (user_id, blog_id),
    CONSTRAINT fk_blog_like_blog FOREIGN KEY (blog_id) REFERENCES blog(id),
    CONSTRAINT fk_blog_like_user FOREIGN KEY (user_id) REFERENCES users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE INDEX idx_blog_created ON blog (created_at, id);
