use sea_orm_migration::prelude::*;

#[derive(DeriveMigrationName)]
pub struct Migration;

#[async_trait::async_trait]
impl MigrationTrait for Migration {
    async fn up(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .alter_table(
                Table::alter()
                    .table(MessageQueueItem::Table)
                    .add_column(
                        ColumnDef::new(MessageQueueItem::ScheduledAt)
                            .timestamp_with_time_zone()
                            .null(),
                    )
                    .to_owned(),
            )
            .await?;
        manager
            .create_index(
                Index::create()
                    .name("idx_message_queue_schedule")
                    .table(MessageQueueItem::Table)
                    .col(MessageQueueItem::ScheduledAt)
                    .col(MessageQueueItem::DeliveryState)
                    .col(MessageQueueItem::OwnerId)
                    .to_owned(),
            )
            .await
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .drop_index(
                Index::drop()
                    .name("idx_message_queue_schedule")
                    .table(MessageQueueItem::Table)
                    .to_owned(),
            )
            .await?;
        manager
            .alter_table(
                Table::alter()
                    .table(MessageQueueItem::Table)
                    .drop_column(MessageQueueItem::ScheduledAt)
                    .to_owned(),
            )
            .await
    }
}

#[derive(DeriveIden)]
enum MessageQueueItem {
    Table,
    ScheduledAt,
    DeliveryState,
    OwnerId,
}
