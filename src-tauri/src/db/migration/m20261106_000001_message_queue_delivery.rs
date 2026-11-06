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
                        ColumnDef::new(MessageQueueItem::DeliveryState)
                            .string()
                            .not_null()
                            .default("pending"),
                    )
                    .to_owned(),
            )
            .await?;
        manager
            .alter_table(
                Table::alter()
                    .table(MessageQueueItem::Table)
                    .add_column(ColumnDef::new(MessageQueueItem::ClaimToken).string().null())
                    .to_owned(),
            )
            .await?;
        manager
            .alter_table(
                Table::alter()
                    .table(MessageQueueItem::Table)
                    .add_column(ColumnDef::new(MessageQueueItem::LastError).text().null())
                    .to_owned(),
            )
            .await?;
        manager
            .create_index(
                Index::create()
                    .name("idx_message_queue_delivery")
                    .table(MessageQueueItem::Table)
                    .col(MessageQueueItem::OwnerId)
                    .col(MessageQueueItem::DeliveryState)
                    .to_owned(),
            )
            .await
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .drop_index(
                Index::drop()
                    .name("idx_message_queue_delivery")
                    .table(MessageQueueItem::Table)
                    .to_owned(),
            )
            .await?;
        for column in [
            MessageQueueItem::LastError,
            MessageQueueItem::ClaimToken,
            MessageQueueItem::DeliveryState,
        ] {
            manager
                .alter_table(
                    Table::alter()
                        .table(MessageQueueItem::Table)
                        .drop_column(column)
                        .to_owned(),
                )
                .await?;
        }
        Ok(())
    }
}

#[derive(DeriveIden)]
enum MessageQueueItem {
    Table,
    OwnerId,
    DeliveryState,
    ClaimToken,
    LastError,
}
