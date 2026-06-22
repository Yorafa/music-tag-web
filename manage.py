#!/usr/bin/env python
"""Django's command-line utility for administrative tasks."""
import os
import sys

# PyMySQL 替代 mysqlclient — 纯 Python MySQL 驱动，零 C 编译依赖
# 只在 settings.py 切到 MySQL 数据库时生效；默认 SQLite 无需此行
import pymysql
pymysql.install_as_MySQLdb()


def main():
    """Run administrative tasks."""
    os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'music_site.settings')
    try:
        from django.core.management import execute_from_command_line
    except ImportError as exc:
        raise ImportError(
            "Couldn't import Django. Are you sure it's installed and "
            "available on your PYTHONPATH environment variable? Did you "
            "forget to activate a virtual environment?"
        ) from exc
    execute_from_command_line(sys.argv)


if __name__ == '__main__':
    main()
