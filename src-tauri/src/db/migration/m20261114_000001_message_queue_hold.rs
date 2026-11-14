use sea_orm_migration::prelude::*;

#[derive(DeriveMigrationName)]
pub struct Migration;

#[async_trait::async_trait]
impl MigrationTrait for Migration {
    async fn up(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        // Why the queue is holding back from auto-draining despite being
        // "open" (`manual_stop` = the user stopped the running turn). Raw SQL
        // like the table's own migration — `message_queue_state` has no
        // SeaORM entity.
        manager
            .get_connection()
            .execute_unprepared(
                "ALTER TABLE message_queue_state ADD COLUMN hold TEXT NULL;",
            )
            .await?;
        Ok(())
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .get_connection()
            .execute_unprepared(
                "ALTER TABLE message_queue_state DROP COLUMN hold;",
            )
            .await?;
        Ok(())
    }
}
