# -*- coding: utf-8 -*-

from __future__ import absolute_import, unicode_literals

import os
import time

# PyMySQL 替代 mysqlclient — 纯 Python MySQL 驱动
# 若 settings.py 中启用 MySQL 数据库，此处必须在 Celery app 初始化之前调用
import pymysql
pymysql.install_as_MySQLdb()

from celery import Celery, platforms
from django.conf import settings

platforms.C_FORCE_ROOT = True

# set the default Django settings module for the 'celery' program.
os.environ.setdefault("DJANGO_SETTINGS_MODULE", "music_site.settings")

app = Celery("music_site")

# Using a string here means the worker don't have to serialize
# the configuration object to child processes.
# - namespace='CELERY' means all celery-related configuration keys
#   should have a `CELERY_` prefix.
app.config_from_object("django.conf:settings")

# Load task modules from all registered Django app configs.
app.autodiscover_tasks(lambda: settings.INSTALLED_APPS)


@app.task(bind=True)
def debug_task(self):
    print("Request: {!r}".format(self.request))
    time.sleep(2)
