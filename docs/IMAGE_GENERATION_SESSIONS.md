# 在线生图会话数据

在线生图的会话、草稿、记录及图片索引保存在 PostgreSQL 的 `image_generation_sessions`、`image_generation_records`、`image_generation_assets` 表中。图片原文件保存在服务端 `./data/image-sessions/`，通过登录用户鉴权后读取；异步任务的原始图片仍保存在 `image_storage.local_dir`（默认 `./data/image-storage/`）。两个目录都必须挂载持久化卷，且多实例部署需要共享同一文件系统。

现有“数据备份”只备份数据库，不包含上述本地文件。要完整备份生图记录，应在同一个维护窗口暂停新生图及上传，备份 PostgreSQL，并同时备份 `./data/image-sessions/` 和已配置的 `image_storage.local_dir`。恢复时先恢复数据库及相同路径下的文件，再启动服务。只恢复数据库会留下无法显示的图片；只恢复图片则没有会话索引。迁移旧浏览器记录请在各浏览器的在线生图页面手动使用“导入此浏览器记录”，导入前确认登录的是原记录所属账号。
