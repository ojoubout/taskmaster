import yaml
from taskmaster.error import error

from taskmaster.program import Program

def parse_config_file():
    try:
        with open('taskmaster.conf') as stream:
            config_file = yaml.safe_load(stream)
            extract_programs(config_file)
    except FileNotFoundError as e:
        error('config file not found')
    except yaml.YAMLError as exc:
        error("can't parse config file: {exc}")

def extract_programs(config):
    if "programs" not in config:
        error("can't parse config file: 'programs' key is missing")
    for key, value in config["programs"].items():
        if "command" not in value:
            error("can't parse config file: '{key}' program requires a command attribute")
        program = Program(value["command"])
        try:
            program.numprocs = value["numprocs"]
            program.umask = value["umask"]
            program.directory = value["directory"]
            program.autostart = value["autostart"]
            program.autorestart = value["autorestart"]
            program.startsecs = value["startsecs"]
            program.startretries = value["startretries"]
            program.stopsignal = value["stopsignal"]
            program.stopwaitsecs = value["stopwaitsecs"]
            program.stdout = value["stdout"]
            program.stderr = value["stderr"]
            program.env = value["env"]
        except KeyError:
            pass