CREATE TABLE movies (
			id           TEXT PRIMARY KEY,
			tmdb_id      INTEGER UNIQUE,
			title        TEXT NOT NULL,
			original_title TEXT DEFAULT '',
			year         INTEGER DEFAULT 0,
			overview     TEXT DEFAULT '',
			tagline      TEXT DEFAULT '',
			runtime      INTEGER DEFAULT 0,
			vote_average REAL DEFAULT 0,
			status       TEXT DEFAULT '',
			imdb_id      TEXT DEFAULT '',
			genres       TEXT DEFAULT '[]',
			poster_path  TEXT DEFAULT '',
			backdrop_path TEXT DEFAULT '',
			monitored    INTEGER DEFAULT 1,
			has_file     INTEGER DEFAULT 0,
			quality_profile_id TEXT DEFAULT '',
			root_folder_path   TEXT DEFAULT '',
			created_at   TEXT NOT NULL,
			updated_at   TEXT NOT NULL
		, collection_id INTEGER DEFAULT 0, collection_name TEXT DEFAULT '', release_date TEXT DEFAULT '');
CREATE TABLE movie_files (
			id         TEXT PRIMARY KEY,
			movie_id   TEXT NOT NULL,
			file_path  TEXT NOT NULL,
			quality    TEXT DEFAULT '',
			size_bytes INTEGER DEFAULT 0,
			container  TEXT DEFAULT '',
			created_at TEXT NOT NULL,
			FOREIGN KEY (movie_id) REFERENCES movies(id) ON DELETE CASCADE
		);
CREATE TABLE tags (
			id TEXT PRIMARY KEY,
			label TEXT UNIQUE NOT NULL,
			created_at TEXT NOT NULL
		);
CREATE TABLE item_tags (
			item_id TEXT NOT NULL,
			tag_id TEXT NOT NULL,
			PRIMARY KEY (item_id, tag_id)
		);
CREATE TABLE history (
			id           TEXT PRIMARY KEY,
			event_type   TEXT NOT NULL,
			item_id      TEXT NOT NULL,
			title        TEXT NOT NULL DEFAULT '',
			source_title TEXT NOT NULL DEFAULT '',
			quality      TEXT NOT NULL DEFAULT '',
			indexer      TEXT NOT NULL DEFAULT '',
			file_path    TEXT NOT NULL DEFAULT '',
			download_id  TEXT NOT NULL DEFAULT '',
			data_json    TEXT NOT NULL DEFAULT '{}',
			created_at   TEXT NOT NULL
		);
CREATE TABLE movie_titles (
			id TEXT PRIMARY KEY,
			movie_id TEXT NOT NULL,
			title TEXT NOT NULL,
			clean_title TEXT NOT NULL,
			source TEXT NOT NULL,
			UNIQUE(movie_id, clean_title),
			FOREIGN KEY (movie_id) REFERENCES movies(id) ON DELETE CASCADE
		);
CREATE TABLE collection_prefs (
			collection_id INTEGER PRIMARY KEY,
			name TEXT DEFAULT '',
			monitored INTEGER DEFAULT 0,
			search_on_add INTEGER DEFAULT 1,
			quality_profile_id TEXT DEFAULT '',
			root_folder_path TEXT DEFAULT '',
			updated_at TEXT NOT NULL
		);
CREATE TABLE movie_content_rating (
			movie_id       TEXT PRIMARY KEY,
			content_rating TEXT NOT NULL,
			source         TEXT NOT NULL,
			updated_at     TEXT NOT NULL,
			FOREIGN KEY (movie_id) REFERENCES movies(id) ON DELETE CASCADE
		);
CREATE INDEX idx_movies_title ON movies(title)
	;
CREATE INDEX idx_movie_files_movie ON movie_files(movie_id)
	;
CREATE INDEX idx_history_created ON history(created_at DESC)
	;
CREATE INDEX idx_history_item ON history(item_id, created_at DESC)
	;
CREATE INDEX idx_movie_titles_clean ON movie_titles(clean_title)
	;
