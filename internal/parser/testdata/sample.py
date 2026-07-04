import os


def hello(name: str) -> None:
    print(name)


class User:
    def __init__(self, name: str):
        self.name = name

    def greet(self) -> str:
        return self.name