use sea_orm_migration::prelude::*;

#[derive(DeriveMigrationName)]
pub struct Migration;

#[async_trait::async_trait]
impl MigrationTrait for Migration {
    async fn up(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager.get_connection().execute_unprepared(
            "CREATE TABLE message_queue_state (
                owner_id TEXT PRIMARY KEY NOT NULL,
                status TEXT NOT NULL DEFAULT 'open',
                active_attempt_id TEXT NULL,
                failed_attempt_id TEXT NULL,
                error TEXT NULL,
                revision INTEGER NOT NULL DEFAULT 0,
                updated_at TEXT NOT NULL
            );
            CREATE TABLE message_queue_attempt (
                id TEXT PRIMARY KEY NOT NULL,
                owner_id TEXT NOT NULL,
                connection_id TEXT NOT NULL,
                message_id TEXT NOT NULL,
                draft_json TEXT NOT NULL,
                recovery_of TEXT NULL,
                status TEXT NOT NULL,
                created_at TEXT NOT NULL,
                completed_at TEXT NULL
            );
            CREATE INDEX idx_message_queue_attempt_owner ON message_queue_attempt(owner_id, created_at);"
        ).await?;
        Ok(())
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .get_connection()
            .execute_unprepared("DROP TABLE message_queue_attempt; DROP TABLE message_queue_state;")
            .await?;
        Ok(())
    }
}
