use sea_orm_migration::prelude::*;

#[derive(DeriveMigrationName)]
pub struct Migration;

#[async_trait::async_trait]
impl MigrationTrait for Migration {
    async fn up(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        // media_provider: the global vendor pool for comic (漫剧) media
        // generation — image / video / tts endpoints with their credentials
        // and vendor-specific extras. Projects reference rows by id; tasks
        // snapshot what they used. Unique (kind, name) so a project's
        // by-name fallback resolution stays deterministic.
        manager
            .create_table(
                Table::create()
                    .table(MediaProvider::Table)
                    .if_not_exists()
                    .col(
                        ColumnDef::new(MediaProvider::Id)
                            .integer()
                            .not_null()
                            .auto_increment()
                            .primary_key(),
                    )
                    .col(ColumnDef::new(MediaProvider::Kind).string().not_null())
                    .col(ColumnDef::new(MediaProvider::Name).string().not_null())
                    .col(ColumnDef::new(MediaProvider::ApiUrl).text().not_null())
                    .col(ColumnDef::new(MediaProvider::ApiKey).text().not_null())
                    .col(ColumnDef::new(MediaProvider::Model).string().not_null())
                    .col(ColumnDef::new(MediaProvider::ExtraJson).text().not_null())
                    .col(
                        ColumnDef::new(MediaProvider::IsDefault)
                            .boolean()
                            .not_null()
                            .default(false),
                    )
                    .col(
                        ColumnDef::new(MediaProvider::CreatedAt)
                            .timestamp_with_time_zone()
                            .not_null(),
                    )
                    .col(
                        ColumnDef::new(MediaProvider::UpdatedAt)
                            .timestamp_with_time_zone()
                            .not_null(),
                    )
                    .to_owned(),
            )
            .await?;

        manager
            .create_index(
                Index::create()
                    .if_not_exists()
                    .name("idx_media_provider_kind_name")
                    .table(MediaProvider::Table)
                    .col(MediaProvider::Kind)
                    .col(MediaProvider::Name)
                    .unique()
                    .to_owned(),
            )
            .await?;

        // comic_generation_task: one durable row per media-generation job
        // (image batch / video clip / tts take / episode compose). Survives
        // restarts; the worker polls the vendor from it and writes finished
        // artifacts into the project's story/ tree.
        manager
            .create_table(
                Table::create()
                    .table(ComicGenerationTask::Table)
                    .if_not_exists()
                    .col(
                        ColumnDef::new(ComicGenerationTask::Id)
                            .integer()
                            .not_null()
                            .auto_increment()
                            .primary_key(),
                    )
                    .col(
                        ColumnDef::new(ComicGenerationTask::FolderId)
                            .integer()
                            .not_null(),
                    )
                    .col(
                        ColumnDef::new(ComicGenerationTask::Kind)
                            .string()
                            .not_null(),
                    )
                    .col(
                        ColumnDef::new(ComicGenerationTask::Status)
                            .string()
                            .not_null(),
                    )
                    .col(
                        ColumnDef::new(ComicGenerationTask::ProviderId)
                            .integer()
                            .null(),
                    )
                    .col(
                        ColumnDef::new(ComicGenerationTask::ProviderName)
                            .string()
                            .null(),
                    )
                    .col(ColumnDef::new(ComicGenerationTask::Model).string().null())
                    .col(
                        ColumnDef::new(ComicGenerationTask::RequestJson)
                            .text()
                            .not_null(),
                    )
                    .col(
                        ColumnDef::new(ComicGenerationTask::ResultJson)
                            .text()
                            .not_null(),
                    )
                    .col(
                        ColumnDef::new(ComicGenerationTask::ArtifactPath)
                            .text()
                            .null(),
                    )
                    .col(
                        ColumnDef::new(ComicGenerationTask::TargetKind)
                            .string()
                            .null(),
                    )
                    .col(ColumnDef::new(ComicGenerationTask::TargetId).text().null())
                    .col(
                        ColumnDef::new(ComicGenerationTask::ErrorKind)
                            .string()
                            .null(),
                    )
                    .col(
                        ColumnDef::new(ComicGenerationTask::ErrorMessage)
                            .text()
                            .null(),
                    )
                    .col(
                        ColumnDef::new(ComicGenerationTask::CandidatesJson)
                            .text()
                            .not_null(),
                    )
                    .col(
                        ColumnDef::new(ComicGenerationTask::CreatedAt)
                            .timestamp_with_time_zone()
                            .not_null(),
                    )
                    .col(
                        ColumnDef::new(ComicGenerationTask::UpdatedAt)
                            .timestamp_with_time_zone()
                            .not_null(),
                    )
                    .to_owned(),
            )
            .await?;

        // "Tasks for a project, newest first" — the workbench task panel and
        // the comic_check_generation long-poll both read along this.
        manager
            .create_index(
                Index::create()
                    .if_not_exists()
                    .name("idx_comic_generation_task_folder")
                    .table(ComicGenerationTask::Table)
                    .col(ComicGenerationTask::FolderId)
                    .to_owned(),
            )
            .await?;

        // The worker's queue scan: pick up queued/running rows per kind.
        manager
            .create_index(
                Index::create()
                    .if_not_exists()
                    .name("idx_comic_generation_task_status")
                    .table(ComicGenerationTask::Table)
                    .col(ComicGenerationTask::Status)
                    .to_owned(),
            )
            .await?;

        Ok(())
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .drop_table(Table::drop().table(ComicGenerationTask::Table).to_owned())
            .await?;
        manager
            .drop_table(Table::drop().table(MediaProvider::Table).to_owned())
            .await
    }
}

#[derive(DeriveIden)]
enum MediaProvider {
    Table,
    Id,
    Kind,
    Name,
    ApiUrl,
    ApiKey,
    Model,
    ExtraJson,
    IsDefault,
    CreatedAt,
    UpdatedAt,
}

#[derive(DeriveIden)]
enum ComicGenerationTask {
    Table,
    Id,
    FolderId,
    Kind,
    Status,
    ProviderId,
    ProviderName,
    Model,
    RequestJson,
    ResultJson,
    ArtifactPath,
    TargetKind,
    TargetId,
    ErrorKind,
    ErrorMessage,
    CandidatesJson,
    CreatedAt,
    UpdatedAt,
}
