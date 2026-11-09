use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "model_switch")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: String,
    pub conversation_id: Option<i32>,
    pub agent_type: String,
    pub connection_id: String,
    pub session_id: Option<String>,
    pub sequence: i64,
    pub created_at: DateTimeUtc,
    /// Immutable label/anchor/outcome snapshot; routing columns stay independently queryable.
    pub record_json: String,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {}
impl ActiveModelBehavior for ActiveModel {}
