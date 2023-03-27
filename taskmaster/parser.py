import yaml
from taskmaster.error import error
from taskmaster.config import Config
from taskmaster.program import Program

def parse_config_file() -> Config:
    conf = Config()
    try:
        with open('taskmaster.conf') as stream:
            config_file = yaml.safe_load(stream)
            conf.programs = extract_programs(config_file)
    except FileNotFoundError as e:
        error('config file not found')
    except yaml.YAMLError as exc:
        error("can't parse config file: {exc}")
    return conf

def extract_programs(config) -> dict:
    programs = {}
    if "programs" not in config:
        error("can't parse config file: 'programs' key is missing")
    for key, value in config["programs"].items():
        if "command" not in value:
            error("can't parse config file: '{key}' program requires a command attribute")
        program = Program(**value)
        programs[key] = program
    return programs
        
