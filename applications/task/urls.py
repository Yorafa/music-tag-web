from rest_framework import routers

from . import views

router = routers.DefaultRouter()
router.register(r"", views.TaskViewSets, basename='task')
router.register(r"record", views.TaskModelViewSets, basename='record')

