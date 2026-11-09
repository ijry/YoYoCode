use sea_orm_migration::prelude::*;

#[derive(DeriveMigrationName)]
pub struct Migration;

#[async_trait::async_trait]
impl MigrationTrait for Migration {
    async fn up(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager.get_connection().execute_unprepared(
            "CREATE TABLE IF NOT EXISTS model_switch (
                id TEXT PRIMARY KEY NOT NULL,
                conversation_id INTEGER REFERENCES conversation(id) ON DELETE CASCADE,
                agent_type TEXT NOT NULL,
                connection_id TEXT NOT NULL,
                session_id TEXT,
                sequence BIGINT NOT NULL,
                created_at TEXT NOT NULL,
                record_json TEXT NOT NULL
            );
            CREATE INDEX IF NOT EXISTS idx_model_switch_conversation ON model_switch(conversation_id, created_at, connection_id, sequence);
            CREATE INDEX IF NOT EXISTS idx_model_switch_connection ON model_switch(connection_id);
            CREATE INDEX IF NOT EXISTS idx_model_switch_session ON model_switch(agent_type, session_id);"
        ).await?;
        Ok(())
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .get_connection()
            .execute_unprepared("DROP TABLE model_switch")
            .await?;
        Ok(())
    }
}
