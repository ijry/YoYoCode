use sea_orm::sea_query::{Expr, OnConflict};
use sea_orm::{
    ColumnTrait, ConnectionTrait, DatabaseConnection, EntityTrait, QueryFilter, QueryOrder, Set,
};

use crate::db::entities::{conversation, model_switch};
use crate::db::error::DbError;
use crate::models::{AgentType, ModelSwitchInfo};

fn agent_db_key(agent: AgentType) -> String {
    serde_json::to_value(agent)
        .expect("AgentType serializes as a string")
        .as_str()
        .expect("AgentType serializes as a string")
        .to_owned()
}

/// Stable id is the only conflict key. Retrying cannot change the recorded outcome
/// or detach a row that has since been linked to its conversation.
pub async fn append(
    conn: &DatabaseConnection,
    conversation_id: Option<i32>,
    agent_type: AgentType,
    record: &ModelSwitchInfo,
) -> Result<(), DbError> {
    let sequence = i64::try_from(record.sequence)
        .map_err(|_| DbError::Validation("model switch sequence overflow".into()))?;
    model_switch::Entity::insert(model_switch::ActiveModel {
        id: Set(record.id.clone()),
        conversation_id: Set(conversation_id),
        agent_type: Set(agent_db_key(agent_type)),
        connection_id: Set(record.connection_id.clone()),
        session_id: Set(record.session_id.clone()),
        sequence: Set(sequence),
        created_at: Set(record.created_at),
        record_json: Set(
            serde_json::to_string(record).map_err(|e| DbError::Validation(e.to_string()))?
        ),
    })
    .on_conflict(
        OnConflict::column(model_switch::Column::Id)
            .do_nothing()
            .to_owned(),
    )
    .do_nothing()
    .exec(conn)
    .await?;
    Ok(())
}

/// Pre-link an existing session without needing another prompt. Agent ids are
/// part of the lookup: two providers may use the same native session id.
pub async fn owner_for_session(
    conn: &DatabaseConnection,
    agent: AgentType,
    session: &str,
) -> Result<Option<(i32, i32)>, DbError> {
    Ok(conversation::Entity::find()
        .filter(conversation::Column::AgentType.eq(agent_db_key(agent)))
        .filter(conversation::Column::ExternalId.eq(session))
        .filter(conversation::Column::DeletedAt.is_null())
        .one(conn)
        .await?
        .map(|row| (row.id, row.folder_id)))
}

pub async fn bind_connection(
    conn: &DatabaseConnection,
    connection_id: &str,
    conversation_id: i32,
    session_id: Option<&str>,
) -> Result<(), DbError> {
    let mut update = model_switch::Entity::update_many()
        .col_expr(
            model_switch::Column::ConversationId,
            Expr::value(conversation_id),
        )
        .filter(model_switch::Column::ConnectionId.eq(connection_id))
        .filter(model_switch::Column::ConversationId.is_null());
    // Records already naming a different session belong to that session, not
    // to a later fork/rollover that happens to reuse this connection.
    if let Some(session) = session_id {
        update = update.filter(
            sea_orm::Condition::any()
                .add(model_switch::Column::SessionId.eq(session))
                .add(model_switch::Column::SessionId.is_null()),
        );
    }
    update.exec(conn).await?;
    if let Some(session) = session_id {
        model_switch::Entity::update_many()
            .col_expr(model_switch::Column::SessionId, Expr::value(session))
            .filter(model_switch::Column::ConnectionId.eq(connection_id))
            .filter(model_switch::Column::ConversationId.eq(conversation_id))
            .filter(model_switch::Column::SessionId.is_null())
            .exec(conn)
            .await?;
    }
    Ok(())
}

pub async fn list_by_conversation(
    conn: &DatabaseConnection,
    conversation_id: i32,
) -> Result<Vec<ModelSwitchInfo>, DbError> {
    let rows = model_switch::Entity::find()
        .filter(model_switch::Column::ConversationId.eq(conversation_id))
        .order_by_asc(model_switch::Column::CreatedAt)
        .order_by_asc(model_switch::Column::ConnectionId)
        .order_by_asc(model_switch::Column::Sequence)
        .order_by_asc(model_switch::Column::Id)
        .all(conn)
        .await?;
    rows.into_iter()
        .map(|row| {
            let mut record: ModelSwitchInfo = serde_json::from_str(&row.record_json)
                .map_err(|e| DbError::Validation(e.to_string()))?;
            record.session_id = row.session_id;
            Ok(record)
        })
        .collect()
}

/// Session splits move the old conversation's records with the history, inside
/// the same transaction as the row re-point. A newly acknowledged record for
/// the successor session must stay with the live row.
pub async fn move_history<C: ConnectionTrait>(
    conn: &C,
    from: i32,
    to: i32,
    successor: &str,
) -> Result<(), sea_orm::DbErr> {
    model_switch::Entity::update_many()
        .col_expr(model_switch::Column::ConversationId, Expr::value(to))
        .filter(model_switch::Column::ConversationId.eq(from))
        .filter(
            sea_orm::Condition::any()
                .add(model_switch::Column::SessionId.ne(successor))
                .add(model_switch::Column::SessionId.is_null()),
        )
        .exec(conn)
        .await?;
    Ok(())
}

/// Fork copies get new stable identities and the destination session. Only
/// records present at the resolved native fork boundary are passed by the caller.
pub async fn copy_to_fork<C: ConnectionTrait>(
    conn: &C,
    destination: i32,
    session: &str,
    ids: &[String],
) -> Result<(), sea_orm::DbErr> {
    if ids.is_empty() {
        return Ok(());
    }
    for row in model_switch::Entity::find()
        .filter(model_switch::Column::Id.is_in(ids.to_vec()))
        .all(conn)
        .await?
    {
        let mut record: ModelSwitchInfo = serde_json::from_str(&row.record_json)
            .map_err(|e| sea_orm::DbErr::Custom(e.to_string()))?;
        // Deterministic across a replay of the fork persistence transaction.
        record.id = format!("{}-fork-{}", record.id, destination);
        record.session_id = Some(session.to_owned());
        model_switch::Entity::insert(model_switch::ActiveModel {
            id: Set(record.id.clone()),
            conversation_id: Set(Some(destination)),
            agent_type: Set(row.agent_type),
            connection_id: Set(record.connection_id.clone()),
            session_id: Set(record.session_id.clone()),
            sequence: Set(row.sequence),
            created_at: Set(record.created_at),
            record_json: Set(serde_json::to_string(&record)
                .map_err(|e| sea_orm::DbErr::Custom(e.to_string()))?),
        })
        .on_conflict(
            OnConflict::column(model_switch::Column::Id)
                .do_nothing()
                .to_owned(),
        )
        .do_nothing()
        .exec(conn)
        .await?;
    }
    Ok(())
}
