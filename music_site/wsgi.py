"""
WSGI config for  task.

It exposes the WSGI callable as a module-level variable named ``application``.

For more information on this file, see
https://docs.djangoproject.com/en/3.2/howto/deployment/wsgi/
"""

import os

# PyMySQL 替代 mysqlclient — 纯 Python MySQL 驱动
# 若 settings.py 中启用 MySQL 数据库，此处必须在 Django 初始化之前调用
import pymysql
pymysql.install_as_MySQLdb()

from django.core.wsgi import get_wsgi_application

os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'music_site.settings')

application = get_wsgi_application()
