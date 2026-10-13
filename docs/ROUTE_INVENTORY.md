# 上游路由全量清单 (Route Inventory)

自动解析自 `_upstream/ref/src-tauri/src/web/router.rs`，共 **523** 条路由、47 个模块。

> 注：个别路由名里含上游品牌词（如 `get_codeg_mcp_service_status`），那是**协议契约的字面量**，
> 客户端按原样调用，不能改名。

图例：

- `[x]` 已注册 · `[ ]` 未实现
- **[P0]** mcode-app 实际调用的路由 —— 无缝迁移的硬门槛，共 **128** 条
- 无标记 = 桌面端/其他端使用，mcode-app 不调用，可后置

> 重新生成：`node scripts/gen_inventory.js`（跟进上游新功能后重跑即可）

## (inline) — 2/2 · P0 1/1

- [x] `/health` **[P0]**
- [x] `/ws/events` `get`

## acp — 59/75 · P0 17/17

- [x] `/acp_get_agent_status` **[P0]**
- [x] `/acp_list_agents` **[P0]**
- [x] `/acp_env_diagnostics`
- [x] `/acp_connect` **[P0]**
- [x] `/acp_disconnect` **[P0]**
- [x] `/acp_touch_connection`
- [x] `/acp_prompt` **[P0]**
- [x] `/acp_preflight`
- [x] `/acp_set_mode`
- [x] `/acp_set_config_option` **[P0]**
- [x] `/acp_goal_control`
- [x] `/acp_describe_agent_options` **[P0]**
- [x] `/acp_cancel` **[P0]**
- [x] `/acp_fork`
- [x] `/acp_stop_async_task`
- [x] `/acp_respond_permission` **[P0]**
- [x] `/acp_answer_question`
- [x] `/acp_answer_plan_approval`
- [x] `/acp_list_connections`
- [x] `/acp_get_session_snapshot` **[P0]**
- [x] `/acp_get_session_snapshot_by_conversation`
- [x] `/acp_find_connection_for_conversation`
- [x] `/acp_clear_binary_cache`
- [x] `/acp_scan_leaked_temp`
- [x] `/acp_reclaim_leaked_temp`
- [x] `/acp_update_agent_preferences`
- [x] `/acp_update_agent_env` **[P0]**
- [x] `/acp_update_agent_config` **[P0]**
- [x] `/acp_update_hermes_config`
- [x] `/acp_cursor_auth_status`
- [x] `/acp_cursor_list_models`
- [x] `/acp_qoder_auth_status`
- [x] `/acp_update_kimi_code_config`
- [x] `/acp_fetch_kimi_models`
- [x] `/acp_update_pi_config`
- [x] `/acp_load_pi_config`
- [ ] `/acp_list_pi_model_capabilities`
- [x] `/acp_load_deepseek_model_catalog`
- [x] `/acp_update_deepseek_model_catalog`
- [x] `/acp_validate_pi_command`
- [ ] `/acp_sync_antigravity_settings`
- [ ] `/acp_antigravity_login_start`
- [ ] `/acp_antigravity_login_finish`
- [ ] `/acp_antigravity_login_cancel`
- [ ] `/acp_antigravity_sign_out`
- [ ] `/acp_pi_project_trust_state`
- [ ] `/acp_pi_set_project_trust`
- [ ] `/acp_pi_acknowledge_project_trust`
- [ ] `/acp_pi_list_trust_entries`
- [ ] `/acp_install_pi_binary`
- [ ] `/acp_uninstall_pi_binary`
- [x] `/acp_download_agent_binary` **[P0]**
- [x] `/acp_install_uv_tool` **[P0]**
- [x] `/acp_detect_agent_local_version`
- [x] `/acp_fetch_agent_latest_release`
- [x] `/acp_prepare_npx_agent` **[P0]**
- [x] `/acp_uninstall_agent` **[P0]**
- [x] `/acp_reorder_agents` **[P0]**
- [x] `/acp_list_custom_agents`
- [x] `/acp_save_custom_agent`
- [x] `/acp_delete_custom_agent`
- [x] `/acp_fetch_registry_catalog`
- [x] `/acp_add_registry_agent`
- [x] `/acp_current_platform`
- [x] `/acp_list_agent_skills`
- [x] `/acp_read_agent_skill`
- [x] `/acp_save_agent_skill`
- [x] `/acp_delete_agent_skill`
- [x] `/opencode_list_plugins`
- [x] `/opencode_provider_catalog`
- [x] `/codex_bundled_catalog`
- [ ] `/opencode_install_plugins`
- [ ] `/opencode_uninstall_plugin`
- [ ] `/codex_request_device_code`
- [ ] `/codex_poll_device_code`

## app_update — 4/4

- [x] `/app_update_state`
- [x] `/perform_app_update`
- [x] `/restart_app`
- [x] `/rollback_app`

## automation — 11/11

- [x] `/automation_list`
- [x] `/automation_get`
- [x] `/automation_runs`
- [x] `/automation_create`
- [x] `/automation_update`
- [x] `/automation_set_enabled`
- [x] `/automation_delete`
- [x] `/automation_mark_seen`
- [x] `/automation_compute_next_run`
- [x] `/automation_run_now`
- [x] `/automation_cancel_run`

## background — 6/6

- [x] `/background_read`
- [x] `/background_set`
- [x] `/background_clear`
- [x] `/background_market_search`
- [x] `/background_market_asset`
- [x] `/background_market_download`

## backup — 2/12

- [ ] `/backup_create_ticket`
- [ ] `/backup_upload`
- [ ] `/backup_prepare_source`
- [ ] `/backup_release_source`
- [ ] `/backup_scan_external_conflicts`
- [ ] `/backup_restore_stage`
- [ ] `/backup_cancel`
- [x] `/backup_list_safety_snapshots`
- [ ] `/backup_rollback`
- [x] `/backup_active_agents`
- [ ] `/backup_discard_pending`
- [ ] `/backup_download/` `get`

## browser_bridge — 3/3

- [x] `/browser_bridge_status`
- [x] `/browser_bridge_open`
- [x] `/browser_bridge_close`

## browser_tools — 2/2

- [x] `/get_browser_tools_settings`
- [x] `/set_browser_tools_settings`

## canvas — 12/12

- [x] `/canvas_list_boards`
- [x] `/canvas_create_board`
- [x] `/canvas_update_board`
- [x] `/canvas_delete_board`
- [x] `/canvas_list_nodes`
- [x] `/canvas_create_node`
- [x] `/canvas_group_into_region`
- [x] `/canvas_update_node`
- [x] `/canvas_move_nodes`
- [x] `/canvas_detach_member`
- [x] `/canvas_delete_node`
- [x] `/canvas_delete_nodes`

## chat_authoring — 2/2

- [x] `/get_chat_authoring_settings`
- [x] `/set_chat_authoring_settings`

## chat_channel — 22/22

- [x] `/list_chat_channels`
- [x] `/create_chat_channel`
- [x] `/update_chat_channel`
- [x] `/delete_chat_channel`
- [x] `/save_chat_channel_token`
- [x] `/get_chat_channel_has_token`
- [x] `/delete_chat_channel_token`
- [x] `/connect_chat_channel`
- [x] `/disconnect_chat_channel`
- [x] `/test_chat_channel`
- [x] `/get_chat_channel_status`
- [x] `/list_chat_channel_messages`
- [x] `/get_chat_command_prefix`
- [x] `/set_chat_command_prefix`
- [x] `/get_chat_event_filter`
- [x] `/set_chat_event_filter`
- [x] `/get_chat_event_webhooks`
- [x] `/set_chat_event_webhooks`
- [x] `/get_chat_message_language`
- [x] `/set_chat_message_language`
- [x] `/weixin_get_qrcode`
- [x] `/weixin_check_qrcode`

## computer — 18/18

- [x] `/computer_available`
- [x] `/computer_status`
- [x] `/computer_request_permission`
- [x] `/computer_open_permission_settings`
- [x] `/computer_reveal_helper`
- [x] `/computer_list_shareable_windows`
- [x] `/computer_window_thumbnail`
- [x] `/computer_share_window`
- [x] `/computer_share_windows`
- [x] `/computer_shared_state`
- [x] `/computer_share_app`
- [x] `/computer_share_screen`
- [x] `/computer_revoke_all`
- [x] `/computer_stop`
- [x] `/computer_stop_key_status`
- [x] `/computer_driver_info`
- [x] `/computer_driver_install`
- [x] `/computer_driver_uninstall`

## computer_tools — 4/4

- [x] `/get_computer_tools_settings`
- [x] `/set_computer_tools_settings`
- [x] `/set_computer_tools_enabled`
- [x] `/set_computer_tools_preferences`

## config_sync — 12/12

- [x] `/config_sync_get_settings`
- [x] `/config_sync_update_settings`
- [x] `/config_sync_get_state`
- [x] `/config_sync_test_connection`
- [x] `/config_sync_upload_now`
- [x] `/config_sync_peek_remote`
- [x] `/config_sync_download_apply`
- [x] `/config_sync_export_content`
- [x] `/config_sync_peek_content`
- [x] `/config_sync_import_content`
- [x] `/config_sync_list_rollbacks`
- [x] `/config_sync_apply_rollback`

## conversation_tags — 8/8

- [x] `/list_conversation_tags`
- [x] `/create_conversation_tag`
- [x] `/update_conversation_tag`
- [x] `/delete_conversation_tag`
- [x] `/reorder_conversation_tags`
- [x] `/update_conversation_tags`
- [x] `/get_conversation_branch_tag`
- [x] `/update_conversation_branch_tag`

## conversations — 21/21 · P0 9/9

- [x] `/list_conversations`
- [x] `/get_conversation`
- [x] `/list_all_conversations` **[P0]**
- [x] `/list_child_conversations`
- [x] `/get_folder_conversation` **[P0]**
- [x] `/get_folder_conversation_turns` **[P0]**
- [x] `/list_opened_tabs` **[P0]**
- [x] `/save_opened_tabs` **[P0]**
- [x] `/import_local_conversations`
- [x] `/scan_importable_sessions`
- [x] `/import_selected_sessions`
- [x] `/list_folders`
- [x] `/get_stats`
- [x] `/get_sidebar_data`
- [x] `/create_conversation` **[P0]**
- [x] `/create_chat_conversation`
- [x] `/create_chat_dir`
- [x] `/update_conversation_status` **[P0]**
- [x] `/update_conversation_title` **[P0]**
- [x] `/update_conversation_pinned`
- [x] `/delete_conversation` **[P0]**

## custom_skills — 10/10

- [x] `/custom_list`
- [x] `/custom_list_all_install_statuses`
- [x] `/custom_apply_links`
- [x] `/custom_read_skill`
- [x] `/custom_create_skill`
- [x] `/custom_save_skill`
- [x] `/custom_duplicate_skill`
- [x] `/custom_import_skill`
- [x] `/custom_import_from_agent`
- [x] `/custom_delete_skills`

## delegation — 2/2 · P0 2/2

- [x] `/get_delegation_settings` **[P0]**
- [x] `/set_delegation_settings` **[P0]**

## event_metrics — 1/1

- [x] `/debug/event_metrics` `get`

## experts — 8/8

- [x] `/experts_list`
- [x] `/experts_get_install_status`
- [x] `/experts_list_all_install_statuses`
- [x] `/experts_link_to_agent`
- [x] `/experts_apply_links`
- [x] `/experts_unlink_from_agent`
- [x] `/experts_read_content`
- [x] `/experts_open_central_dir`

## feedback — 3/3 · P0 2/2

- [x] `/get_feedback_settings` **[P0]**
- [x] `/set_feedback_settings` **[P0]**
- [x] `/submit_session_feedback`

## files — 11/11 · P0 2/2

- [x] `/read_file_preview` **[P0]**
- [x] `/read_file_base64`
- [x] `/read_workspace_file_base64`
- [x] `/read_file_for_edit`
- [x] `/save_file_content`
- [x] `/save_file_copy`
- [x] `/rename_file_tree_entry`
- [x] `/move_file_tree_entry`
- [x] `/delete_file_tree_entry`
- [x] `/create_file_tree_entry` **[P0]**
- [x] `/upload_attachment`

## folder_commands — 6/6

- [x] `/list_folder_commands`
- [x] `/create_folder_command`
- [x] `/update_folder_command`
- [x] `/delete_folder_command`
- [x] `/reorder_folder_commands`
- [x] `/bootstrap_folder_commands_from_package_json`

## folder_links — 6/6

- [x] `/list_folder_links`
- [x] `/preview_folder_links`
- [x] `/create_folder_links`
- [x] `/rename_folder_link`
- [x] `/repair_folder_link`
- [x] `/remove_folder_link`

## folders — 38/38 · P0 8/8

- [x] `/load_folder_history`
- [x] `/list_open_folders`
- [x] `/list_open_folder_details` **[P0]**
- [x] `/list_all_folder_details`
- [x] `/get_folder`
- [x] `/open_folder` **[P0]**
- [x] `/open_in_code`
- [x] `/open_worktree_folder`
- [x] `/resolve_worktree_folder`
- [x] `/open_folder_in_workspace`
- [x] `/open_folder_by_id` **[P0]**
- [x] `/remove_folder_from_workspace`
- [x] `/list_folder_groups`
- [x] `/create_folder_group`
- [x] `/update_folder_group`
- [x] `/delete_folder_group`
- [x] `/apply_sidebar_layout`
- [x] `/set_folder_group`
- [x] `/update_folder_color`
- [x] `/update_folder_alias`
- [x] `/update_folder_default_agent`
- [x] `/add_folder_to_history`
- [x] `/remove_folder_from_history`
- [x] `/create_folder_directory` **[P0]**
- [x] `/get_git_branch` **[P0]**
- [x] `/get_git_head`
- [x] `/get_home_directory` **[P0]**
- [x] `/list_directory_entries` **[P0]**
- [x] `/create_directory`
- [x] `/list_directory_with_files`
- [x] `/get_file_tree` **[P0]**
- [x] `/list_workspace_files`
- [x] `/open_settings_window`
- [x] `/open_commit_window`
- [x] `/open_import_sessions_window`
- [x] `/open_merge_window`
- [x] `/open_stash_window`
- [x] `/open_push_window`

## forge — 17/17 · P0 17/17

- [x] `/folder_forge_remote` **[P0]**
- [x] `/forge_list_issues` **[P0]**
- [x] `/forge_tab_count` **[P0]**
- [x] `/forge_list_labels` **[P0]**
- [x] `/forge_list_comments` **[P0]**
- [x] `/forge_create_comment` **[P0]**
- [x] `/forge_set_item_state` **[P0]**
- [x] `/forge_create_issue` **[P0]**
- [x] `/forge_change_detail` **[P0]**
- [x] `/forge_change_files` **[P0]**
- [x] `/forge_identity` **[P0]**
- [x] `/forge_merge_options` **[P0]**
- [x] `/forge_merge_change` **[P0]**
- [x] `/work_task_create_from_forge` **[P0]**
- [x] `/work_task_lookup_by_source` **[P0]**
- [x] `/forge_settings_get` **[P0]**
- [x] `/forge_settings_set` **[P0]**

## git — 52/52 · P0 13/13

- [x] `/git_status` **[P0]**
- [x] `/git_init`
- [x] `/git_log` **[P0]**
- [x] `/git_current_user`
- [x] `/git_commit_files`
- [x] `/git_search_authors`
- [x] `/git_list_all_branches` **[P0]**
- [x] `/git_list_branches`
- [x] `/git_commit_branches` **[P0]**
- [x] `/git_show_file`
- [x] `/git_show_file_base64`
- [x] `/git_diff` **[P0]**
- [x] `/git_diff_with_branch`
- [x] `/git_show_diff` **[P0]**
- [x] `/git_list_remotes`
- [x] `/git_add_remote`
- [x] `/git_remove_remote`
- [x] `/git_set_remote_url`
- [x] `/git_new_branch` **[P0]**
- [x] `/git_checkout` **[P0]**
- [x] `/git_reset` **[P0]**
- [x] `/git_merge`
- [x] `/git_rebase`
- [x] `/git_worktree_add`
- [x] `/git_push_info` **[P0]**
- [x] `/git_start_pull_merge`
- [x] `/git_has_merge_head`
- [x] `/git_is_tracked`
- [x] `/git_rollback_file`
- [x] `/git_add_files`
- [x] `/git_list_conflicts`
- [x] `/git_conflict_file_versions`
- [x] `/git_resolve_conflict`
- [x] `/git_abort_operation`
- [x] `/git_continue_operation`
- [x] `/git_stash_push`
- [x] `/git_stash_pop`
- [x] `/git_stash_list`
- [x] `/git_stash_apply`
- [x] `/git_stash_drop`
- [x] `/git_stash_clear`
- [x] `/git_stash_show`
- [x] `/git_pull` **[P0]**
- [x] `/git_push` **[P0]**
- [x] `/git_fetch`
- [x] `/git_update_branch`
- [x] `/git_commit` **[P0]**
- [x] `/git_fetch_remote`
- [x] `/git_delete_branch`
- [x] `/git_remove_worktree`
- [x] `/git_delete_remote_branch`
- [x] `/clone_repository`

## logging — 5/5

- [x] `/get_log_settings`
- [x] `/set_log_settings`
- [x] `/get_recent_logs`
- [x] `/list_log_files`
- [x] `/read_log_file`

## mcp — 8/8

- [x] `/mcp_scan_local`
- [x] `/mcp_list_marketplaces`
- [x] `/mcp_search_marketplace`
- [x] `/mcp_get_marketplace_server_detail`
- [x] `/mcp_install_from_marketplace`
- [x] `/mcp_upsert_local_server`
- [x] `/mcp_set_server_apps`
- [x] `/mcp_remove_server`

## mcp_service — 3/3

- [x] `/get_codeg_mcp_service_status`
- [x] `/start_codeg_mcp_service`
- [x] `/set_codeg_mcp_tool_group`

## model_provider — 4/4 · P0 4/4

- [x] `/list_model_providers` **[P0]**
- [x] `/create_model_provider` **[P0]**
- [x] `/update_model_provider` **[P0]**
- [x] `/delete_model_provider` **[P0]**

## office_tools — 12/14

- [x] `/officecli_detect`
- [x] `/officecli_install`
- [x] `/officecli_uninstall`
- [x] `/officecli_list_skills`
- [x] `/officecli_sync_skills`
- [x] `/officecli_skill_link_to_agent`
- [x] `/officecli_skill_unlink_from_agent`
- [x] `/officecli_skill_get_install_status`
- [x] `/officecli_skill_list_all_install_statuses`
- [x] `/officecli_skill_apply_links`
- [x] `/officecli_skill_read_content`
- [x] `/officecli_render_html`
- [ ] `/start_office_watch`
- [ ] `/stop_office_watch`

## office_watch_proxy — 0/3

- [ ] `/office-watch-proxy/` `any`
- [ ] `/office-watch-proxy/` `any`
- [ ] `/office-watch-proxy/` `any`

## pet — 19/19 · P0 1/1

- [x] `/pet_list`
- [x] `/pet_get`
- [x] `/pet_read_spritesheet`
- [x] `/pet_add`
- [x] `/pet_update_meta`
- [x] `/pet_replace_sprite`
- [x] `/pet_delete`
- [x] `/pet_list_importable_codex`
- [x] `/pet_import_codex`
- [x] `/pet_codex_import_available`
- [x] `/pet_get_settings`
- [x] `/pet_set_active`
- [x] `/pet_save_window_state`
- [x] `/pet_marketplace_list`
- [x] `/pet_marketplace_install`
- [x] `/pet_marketplace_asset`
- [x] `/pet_celebrate`
- [x] `/pet_get_current_state`
- [x] `/pet_list_active_sessions` **[P0]**

## project_boot — 5/5

- [x] `/detect_package_manager`
- [x] `/create_shadcn_project`
- [x] `/detect_hyperframes_skills`
- [x] `/install_hyperframes_skills`
- [x] `/create_hyperframes_project`

## question — 2/2 · P0 2/2

- [x] `/get_question_settings` **[P0]**
- [x] `/set_question_settings` **[P0]**

## quick_messages — 5/5 · P0 4/4

- [x] `/quick_messages_list` **[P0]**
- [x] `/quick_messages_create` **[P0]**
- [x] `/quick_messages_update` **[P0]**
- [x] `/quick_messages_delete` **[P0]**
- [x] `/quick_messages_reorder`

## science — 8/8

- [x] `/science_list`
- [x] `/science_get_install_status`
- [x] `/science_list_all_install_statuses`
- [x] `/science_link_to_agent`
- [x] `/science_apply_links`
- [x] `/science_unlink_from_agent`
- [x] `/science_read_content`
- [x] `/science_open_central_dir`

## session_info — 2/2

- [x] `/get_session_info_settings`
- [x] `/set_session_info_settings`

## system_settings — 8/8 · P0 2/2

- [x] `/get_system_proxy_settings`
- [x] `/get_system_terminal_settings`
- [x] `/get_available_terminal_shells`
- [x] `/probe_terminal_shell_path`
- [x] `/update_system_proxy_settings`
- [x] `/update_system_language_settings` **[P0]**
- [x] `/update_system_terminal_settings`
- [x] `/get_system_language_settings` **[P0]**

## terminal — 6/6 · P0 5/5

- [x] `/terminal_spawn` **[P0]**
- [x] `/terminal_write` **[P0]**
- [x] `/terminal_resize` **[P0]**
- [x] `/terminal_snapshot`
- [x] `/terminal_kill` **[P0]**
- [x] `/terminal_list` **[P0]**

## token_usage — 4/4

- [x] `/token_usage_report`
- [x] `/token_usage_facets`
- [x] `/token_usage_status`
- [x] `/token_usage_sync`

## version_control — 12/12 · P0 10/10

- [x] `/detect_git` **[P0]**
- [x] `/test_git_path`
- [x] `/get_git_settings` **[P0]**
- [x] `/update_git_settings` **[P0]**
- [x] `/get_github_accounts` **[P0]**
- [x] `/update_github_accounts` **[P0]**
- [x] `/validate_github_token` **[P0]**
- [x] `/validate_gitlab_token` **[P0]**
- [x] `/validate_gitea_token`
- [x] `/save_account_token` **[P0]**
- [x] `/get_account_token` **[P0]**
- [x] `/delete_account_token` **[P0]**

## web_server — 8/8

- [x] `/get_web_server_status`
- [x] `/get_web_service_config`
- [x] `/update_web_service_config`
- [x] `/start_web_server`
- [x] `/stop_web_server`
- [x] `/probe_web_service_port`
- [x] `/check_app_update`
- [x] `/app_update_status`

## work_task — 31/31 · P0 29/29

- [x] `/work_task_list` **[P0]**
- [x] `/work_task_get` **[P0]**
- [x] `/work_task_events` **[P0]**
- [x] `/work_task_attention_count`
- [x] `/work_task_create` **[P0]**
- [x] `/work_task_update` **[P0]**
- [x] `/work_task_reorder` **[P0]**
- [x] `/work_task_delete` **[P0]**
- [x] `/work_task_start` **[P0]**
- [x] `/work_task_start_all`
- [x] `/work_task_retry` **[P0]**
- [x] `/work_task_requeue` **[P0]**
- [x] `/work_task_schedule` **[P0]**
- [x] `/work_task_return` **[P0]**
- [x] `/work_task_deliver_pr` **[P0]**
- [x] `/work_task_cancel` **[P0]**
- [x] `/work_task_merge` **[P0]**
- [x] `/work_task_merge_unqueue` **[P0]**
- [x] `/work_task_complete` **[P0]**
- [x] `/work_task_archive` **[P0]**
- [x] `/work_task_cleanup` **[P0]**
- [x] `/work_task_diff` **[P0]**
- [x] `/work_task_changed_files` **[P0]**
- [x] `/work_task_settings_get` **[P0]**
- [x] `/work_task_settings_effective` **[P0]**
- [x] `/work_task_settings_get_own` **[P0]**
- [x] `/work_task_settings_set` **[P0]**
- [x] `/work_task_settings_delete` **[P0]**
- [x] `/work_task_template_list` **[P0]**
- [x] `/work_task_template_save` **[P0]**
- [x] `/work_task_template_delete` **[P0]**

## workspace_files — 5/5

- [x] `/upload_workspace_file`
- [x] `/workspace_download_ticket`
- [x] `/download_workspace_file`
- [x] `/download_workspace_dir`
- [x] `/workspace_download/` `get`

## workspace_state — 3/3

- [x] `/start_workspace_state_stream`
- [x] `/stop_workspace_state_stream`
- [x] `/get_workspace_snapshot`

## 汇总

- 全部路由：492/523
- P0（mcode-app 依赖）：128/128

| 模块 | 已实现 | P0 进度 |
|---|---|---|
| (inline) | 2/2 | 1/1 |
| acp | 59/75 | 17/17 |
| app_update | 4/4 | — |
| automation | 11/11 | — |
| background | 6/6 | — |
| backup | 2/12 | — |
| browser_bridge | 3/3 | — |
| browser_tools | 2/2 | — |
| canvas | 12/12 | — |
| chat_authoring | 2/2 | — |
| chat_channel | 22/22 | — |
| computer | 18/18 | — |
| computer_tools | 4/4 | — |
| config_sync | 12/12 | — |
| conversation_tags | 8/8 | — |
| conversations | 21/21 | 9/9 |
| custom_skills | 10/10 | — |
| delegation | 2/2 | 2/2 |
| event_metrics | 1/1 | — |
| experts | 8/8 | — |
| feedback | 3/3 | 2/2 |
| files | 11/11 | 2/2 |
| folder_commands | 6/6 | — |
| folder_links | 6/6 | — |
| folders | 38/38 | 8/8 |
| forge | 17/17 | 17/17 |
| git | 52/52 | 13/13 |
| logging | 5/5 | — |
| mcp | 8/8 | — |
| mcp_service | 3/3 | — |
| model_provider | 4/4 | 4/4 |
| office_tools | 12/14 | — |
| office_watch_proxy | 0/3 | — |
| pet | 19/19 | 1/1 |
| project_boot | 5/5 | — |
| question | 2/2 | 2/2 |
| quick_messages | 5/5 | 4/4 |
| science | 8/8 | — |
| session_info | 2/2 | — |
| system_settings | 8/8 | 2/2 |
| terminal | 6/6 | 5/5 |
| token_usage | 4/4 | — |
| version_control | 12/12 | 10/10 |
| web_server | 8/8 | — |
| work_task | 31/31 | 29/29 |
| workspace_files | 5/5 | — |
| workspace_state | 3/3 | — |
